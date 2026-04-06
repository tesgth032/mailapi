package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"mailapi/internal/cache"
	"mailapi/internal/model"
	"mailapi/internal/queue"
	"mailapi/internal/storage"
	"mailapi/internal/store"

	"github.com/emersion/go-message/mail"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const maxMIMEDepth = 50

// successLogSampleEvery 控制 Worker 成功路径日志采样频率：1=全量，100=百分之一。
// 目的：高吞吐下避免成功日志成为 CPU/IO 热点。
const successLogSampleEvery = 100

var successLogCounter atomic.Uint64

type Worker struct {
	store   store.Interface
	cache   cache.Interface
	queue   queue.Interface
	storage storage.Interface

	messageTTL time.Duration
	objGCKey   string
	// objGCStartAfter 用于对象存储增量扫描（ListObjects StartAfter）。
	objGCStartAfter atomic.Pointer[string]

	// storageCleanupSem 用于限制后台对象清理并发，避免异常/重复消费导致 goroutine 与网络请求爆炸。
	storageCleanupSem chan struct{}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.n += int64(n)
	return n, err
}

type sseMessageNotification struct {
	Type    string        `json:"@type"`
	ID      string        `json:"id"`
	Subject string        `json:"subject"`
	From    model.Address `json:"from"`
	Intro   string        `json:"intro"`
	Seen    bool          `json:"seen"`
}

func New(s store.Interface, c cache.Interface, q queue.Interface, st storage.Interface, messageTTL time.Duration) *Worker {
	// 对象存储删除通常是网络 IO，适当放大并发；但仍需上限，避免异常/重复消费场景把 CPU/FD/带宽打爆。
	maxCleanup := runtime.GOMAXPROCS(0) * 4
	if maxCleanup < 4 {
		maxCleanup = 4
	}
	if maxCleanup > 64 {
		maxCleanup = 64
	}

	w := &Worker{
		store:             s,
		cache:             c,
		queue:             q,
		storage:           st,
		messageTTL:        messageTTL,
		objGCKey:          "mailapi:objgc:lock",
		storageCleanupSem: make(chan struct{}, maxCleanup),
	}
	startAfter := ""
	w.objGCStartAfter.Store(&startAfter)
	return w
}

func (w *Worker) Start(ctx context.Context) error {
	log.Println("Worker started, consuming messages...")
	// 对象存储 GC：清理 Mongo TTL 过期/软删除后遗留的对象，避免存储无限增长。
	// 默认仅在 message TTL 启用时运行；通过分布式锁保证集群中最多 1 个 worker 执行。
	if w.messageTTL > 0 {
		go w.objectGCLoop(ctx)
	}
	return w.queue.Consume(ctx, w.processMessage)
}

var objGCUnlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

func (w *Worker) objectGCLoop(ctx context.Context) {
	// 给系统一点时间完成启动（尤其是 MinIO/NATS），避免启动即触发扫描。
	timer := time.NewTimer(30 * time.Second)
	select {
	case <-ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
	}

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		w.objectGCOnce(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) objectGCOnce(ctx context.Context) {
	if w == nil || w.storage == nil || w.cache == nil || w.store == nil {
		return
	}
	client := w.cache.Client()
	if client == nil {
		return
	}

	// 分布式锁：避免多 worker 并行扫描同一个 bucket。
	lockVal := fmt.Sprintf("%d", time.Now().UnixNano())
	lockCtx, lockCancel := context.WithTimeout(ctx, 2*time.Second)
	locked, err := client.SetNX(lockCtx, w.objGCKey, lockVal, 10*time.Minute).Result()
	lockCancel()
	if err != nil || !locked {
		return
	}
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = objGCUnlockScript.Run(unlockCtx, client, []string{w.objGCKey}, lockVal).Err()
		unlockCancel()
	}()

	// 整体时间预算：必须小于锁过期时间，避免锁过期后并行多 worker 扫描/删除造成负载放大。
	runCtx, runCancel := context.WithTimeout(ctx, 8*time.Minute)
	defer runCancel()

	startAfterPtr := w.objGCStartAfter.Load()
	startAfter := ""
	if startAfterPtr != nil {
		startAfter = *startAfterPtr
	}

	gcCtx, gcCancel := context.WithTimeout(runCtx, 2*time.Minute)
	ids, nextStartAfter, err := w.storage.ListMessageIDs(gcCtx, startAfter, 20000)
	gcCancel()
	if err != nil {
		log.Printf("WARN: object GC list failed: %v", err)
		return
	}

	// 更新 cursor（增量扫描）。
	if nextStartAfter != startAfter {
		w.objGCStartAfter.Store(&nextStartAfter)
	}

	if len(ids) == 0 {
		return
	}

	existsCtx, existsCancel := context.WithTimeout(runCtx, 30*time.Second)
	existsSet, err := w.store.ExistingMessageIDs(existsCtx, ids)
	existsCancel()
	if err != nil {
		log.Printf("WARN: object GC existence check failed: %v", err)
		return
	}

	deletedMsgs := 0
	for _, id := range ids {
		if runCtx.Err() != nil {
			break
		}
		if _, ok := existsSet[id]; ok {
			continue
		}

		delCtx, delCancel := context.WithTimeout(runCtx, 2*time.Minute)
		if err := w.storage.DeleteByMessage(delCtx, id); err != nil {
			log.Printf("WARN: object GC delete message=%s failed: %v", id, err)
		} else {
			deletedMsgs++
		}
		delCancel()

		// 单次运行做上限保护，避免长时间占用资源。
		if deletedMsgs >= 200 {
			break
		}
	}

	if deletedMsgs > 0 {
		log.Printf("Object GC deleted %d orphan message object prefixes (scanned IDs=%d, next=%q)", deletedMsgs, len(ids), nextStartAfter)
	}
}

func (w *Worker) tryAsyncStorageCleanup(messageID string) {
	if w == nil || w.storage == nil || w.storageCleanupSem == nil {
		return
	}

	select {
	case w.storageCleanupSem <- struct{}{}:
		go func() {
			defer func() { <-w.storageCleanupSem }()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_ = w.storage.DeleteByMessage(ctx, messageID)
			cancel()
		}()
	default:
		// 并发已满：跳过本次清理，依赖后台 object GC 兜底。
	}
}

func (w *Worker) processMessage(ctx context.Context, incoming *model.IncomingEmail) error {
	if incoming == nil {
		return queue.Permanent(fmt.Errorf("nil incoming email"))
	}
	if len(incoming.To) == 0 {
		return queue.Permanent(fmt.Errorf("no recipients"))
	}

	// 去重收件人（防御：避免同一邮件 payload 中重复收件人导致重复入库）。
	uniqueTo := incoming.To
	if len(incoming.To) > 1 {
		m := make(map[string]struct{}, len(incoming.To))
		out := make([]string, 0, len(incoming.To))
		for _, r := range incoming.To {
			r = strings.TrimSpace(strings.ToLower(r))
			if r == "" {
				continue
			}
			if _, ok := m[r]; ok {
				continue
			}
			m[r] = struct{}{}
			out = append(out, r)
		}
		if len(out) > 0 {
			uniqueTo = out
		}
	}

	var transientErr error
	permanentFailures := 0

	for _, recipient := range uniqueTo {
		err := w.processForRecipient(ctx, incoming, recipient)
		if err == nil {
			continue
		}

		if queue.IsPermanent(err) {
			permanentFailures++
			log.Printf("WARN: permanent failure for recipient=%s: %v", recipient, err)
			continue
		}

		log.Printf("ERROR: processing for recipient=%s: %v", recipient, err)
		if transientErr == nil {
			transientErr = err
		}
	}

	if transientErr != nil {
		return transientErr
	}
	if permanentFailures > 0 && permanentFailures == len(uniqueTo) {
		return queue.Permanent(fmt.Errorf("all recipients failed permanently"))
	}
	return nil
}

func readPartStringLimited(r io.Reader, limit int64) (string, error) {
	var b strings.Builder
	// 小预热，避免极小字符串时频繁扩容；大文本会自动增长。
	b.Grow(4 * 1024)
	_, err := io.Copy(&b, io.LimitReader(r, limit))
	if err != nil {
		return "", err
	}
	// strings.Builder.String() 不会再复制底层字节，能显著减少内存与 CPU。
	return b.String(), nil
}

func (w *Worker) processForRecipient(ctx context.Context, incoming *model.IncomingEmail, recipient string) error {
	// 仅查询 account _id（投影），避免在 Worker 高并发路径中反复解码 password/quota 等无关字段。
	accountID, err := w.store.GetAccountIDByAddress(ctx, recipient)
	if err != nil {
		// 地址不存在通常是永久错误（账号 TTL 到期或已删除）。
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrAccountDeleted) {
			return queue.Permanent(fmt.Errorf("account lookup for %s: %w", recipient, err))
		}
		return fmt.Errorf("account lookup for %s: %w", recipient, err)
	}

	// 幂等去重：同一个 JetStream（stream,seq）对同一账号只处理一次。
	if strings.TrimSpace(incoming.IngestStream) != "" && incoming.IngestSeq > 0 {
		exists, err := w.store.HasMessageByIngest(ctx, accountID, incoming.IngestStream, incoming.IngestSeq)
		if err != nil {
			return fmt.Errorf("ingest dedup check for %s: %w", recipient, err)
		}
		if exists {
			return nil
		}
	}

	// 配额预占用：在做 MIME 解析与对象存储上传前先做原子 used+delta，避免在超额时白做大量 CPU/IO。
	// delta 以“原始邮件字节数（RFC822）”计。若后续处理失败/幂等冲突，会回滚该预占用。
	delta := int64(len(incoming.RawMessage))
	reserved := false
	committed := false
	if delta > 0 {
		ok, err := w.store.TryReserveAccountUsed(ctx, accountID, delta)
		if err != nil {
			return fmt.Errorf("reserve quota for %s: %w", recipient, err)
		}
		if !ok {
			// used 可能因 TTL 自动删除/异常回滚/崩溃导致漂移，这里做一次纠偏后再重试。
			fixCtx, fixCancel := context.WithTimeout(ctx, 10*time.Second)
			_, _ = w.store.RecalculateAccountUsed(fixCtx, accountID)
			fixCancel()

			ok2, err2 := w.store.TryReserveAccountUsed(ctx, accountID, delta)
			if err2 != nil {
				return fmt.Errorf("reserve quota for %s: %w", recipient, err2)
			}
			if !ok2 {
				return queue.Permanent(fmt.Errorf("quota exceeded for %s", recipient))
			}
		}
		reserved = true
	}
	defer func() {
		if !reserved || committed || delta <= 0 {
			return
		}
		// 回滚必须尽量成功：用后台 ctx + 超时，避免上游 ctx 已取消导致 used 永久漂移。
		relCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = w.store.UpdateAccountUsed(relCtx, accountID, -delta)
		cancel()
	}()

	// Parse the MIME message
	reader := bytes.NewReader(incoming.RawMessage)
	mr, err := mail.CreateReader(reader)
	if err != nil {
		return queue.Permanent(fmt.Errorf("create mail reader: %w", err))
	}
	defer mr.Close()

	header := mr.Header

	// Extract envelope info
	subject, _ := header.Subject()
	msgID, _ := header.MessageID()
	fromAddrs, _ := header.AddressList("From")
	toAddrs, _ := header.AddressList("To")
	ccAddrs, _ := header.AddressList("Cc")

	now := time.Now()
	retentionDate := now.Add(168 * time.Hour)
	if w.messageTTL > 0 {
		retentionDate = now.Add(w.messageTTL)
	}

	msgOID := bson.NewObjectID()
	msg := &model.Message{
		ID:            msgOID,
		AccountID:     accountID,
		MsgID:         msgID,
		Subject:       subject,
		From:          convertAddress(fromAddrs),
		To:            convertAddresses(toAddrs),
		Cc:            convertAddresses(ccAddrs),
		Size:          int64(len(incoming.RawMessage)),
		Seen:          false,
		IsDeleted:     false,
		IngestStream:  incoming.IngestStream,
		IngestSeq:     incoming.IngestSeq,
		Retention:     true,
		RetentionDate: retentionDate,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// rawMessage 优先存放到对象存储，避免 Mongo 存大字段导致性能与存储成本飙升。
	// 如果对象存储不可用，回退到写入 Mongo（保证功能正确性）。
	if err := w.storage.UploadRawMessage(ctx, msgOID.Hex(), incoming.RawMessage); err != nil {
		log.Printf("WARN: upload raw message failed, fallback to Mongo rawMessage (id=%s): %v", msgOID.Hex(), err)
		msg.RawMessage = incoming.RawMessage
	}

	var textBody string
	var htmlBodies []string
	var attachments []model.Attachment
	depth := 0

	// Iterate over MIME parts
	for {
		if depth > maxMIMEDepth {
			log.Printf("WARN: max MIME depth reached for message %s", msgID)
			break
		}
		depth++

		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("WARN: error reading MIME part: %v", err)
			break
		}

		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			// 10MB limit per part。使用 strings.Builder 读到 string，避免 []byte -> string 的额外拷贝。
			body, err := readPartStringLimited(p.Body, 10<<20)
			if err != nil {
				log.Printf("WARN: error reading inline part: %v", err)
				continue
			}

			switch {
			case strings.HasPrefix(ct, "text/plain"):
				textBody = body
			case strings.HasPrefix(ct, "text/html"):
				htmlBodies = append(htmlBodies, body)
			}

		case *mail.AttachmentHeader:
			filename, _ := h.Filename()
			ct, _, _ := h.ContentType()
			if filename == "" {
				filename = "unnamed"
			}

			attID := bson.NewObjectID().Hex()
			att := model.Attachment{
				ID:               attID,
				Filename:         filename,
				ContentType:      ct,
				Disposition:      "attachment",
				TransferEncoding: "base64",
				Size:             0,
			}

			// Upload to MinIO (streaming, avoids loading whole attachment into memory)
			limited := io.LimitReader(p.Body, 20<<20) // 20MB limit per attachment
			cr := &countingReader{r: limited}
			if err := w.storage.UploadReader(ctx, msgOID.Hex(), attID, filename, ct, cr, -1); err != nil {
				log.Printf("ERROR uploading attachment %s: %v", filename, err)
				continue
			}
			att.Size = cr.n

			attachments = append(attachments, att)
		}
	}

	msg.Text = textBody
	msg.HTML = htmlBodies
	msg.Attachments = attachments
	msg.HasAttachments = len(attachments) > 0

	// Generate intro (first 100 chars of text body)
	msg.Intro = generateIntro(textBody, 100)

	// Store in MongoDB
	if err := w.store.CreateMessage(ctx, msg); err != nil {
		// 对象存储已上传的 raw/attachments 可能会成为孤儿；尽量后台清理。
		w.tryAsyncStorageCleanup(msgOID.Hex())

		// 幂等冲突：说明另一 worker 已处理成功；这里视为成功并返回（同时清理本次重复上传的对象）。
		if errors.Is(err, store.ErrDuplicateKey) {
			return nil
		}

		return fmt.Errorf("store message: %w", err)
	}

	committed = true

	// Publish SSE notification via Redis pub/sub
	// 用 struct 代替 map：避免 map key 排序与反射开销（高并发下更省 CPU/分配）。
	notification := sseMessageNotification{
		Type:    "Message",
		ID:      msgOID.Hex(),
		Subject: subject,
		From:    msg.From,
		Intro:   msg.Intro,
		Seen:    false,
	}
	data, _ := json.Marshal(notification)
	if err := w.cache.Publish(ctx, accountID.Hex(), string(data)); err != nil {
		log.Printf("WARN: failed to publish SSE event: %v", err)
	}

	if successLogSampleEvery > 0 {
		n := successLogCounter.Add(1)
		if n%uint64(successLogSampleEvery) == 0 {
			log.Printf("Processed message id=%s for=%s subject=%q", msgOID.Hex(), recipient, subject)
		}
	}
	return nil
}

func convertAddress(addrs []*mail.Address) model.Address {
	if len(addrs) == 0 {
		return model.Address{}
	}
	return model.Address{Name: addrs[0].Name, Address: addrs[0].Address}
}

func convertAddresses(addrs []*mail.Address) []model.Address {
	result := make([]model.Address, 0, len(addrs))
	for _, a := range addrs {
		result = append(result, model.Address{Name: a.Name, Address: a.Address})
	}
	return result
}

func generateIntro(text string, maxLen int) string {
	if maxLen < 0 {
		maxLen = 0
	}

	// strings.Fields 会分配大量切片/字符串片段；这里改为流式压缩空白，
	// 并在超过 maxLen 后尽早停止，显著降低大正文邮件的 CPU 与分配。
	normalized, truncated := normalizeWhitespaceLimited(text, maxLen)
	if truncated {
		return normalized + "..."
	}
	return normalized
}

func normalizeWhitespaceLimited(s string, maxLen int) (out string, truncated bool) {
	var b strings.Builder
	if maxLen > 0 && maxLen < 1024 {
		b.Grow(maxLen + 64)
	} else {
		// 避免对超大正文直接 Grow(len(s)) 带来的瞬时内存峰值。
		b.Grow(4 * 1024)
	}

	wrote := 0
	started := false
	inSpace := true // 以“在空白中”开始，用于丢弃前导空白
	atLimit := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inSpace = true
			continue
		}

		// 已写满：继续扫描是否还有非空白字符；若有则截断。
		if atLimit {
			return b.String(), true
		}

		// maxLen==0：只要存在非空白字符，就必然需要 "..."
		if maxLen == 0 {
			return "", true
		}

		rl := utf8.RuneLen(r)
		if rl < 0 {
			rl = 1
		}

		// 需要插入空格：只有当“空格+当前 rune 都能写入”时才写，避免末尾出现孤立空格。
		if inSpace && started {
			if wrote+1+rl > maxLen {
				return b.String(), true
			}
			b.WriteByte(' ')
			wrote++
		}

		if wrote+rl > maxLen {
			return b.String(), true
		}

		inSpace = false
		b.WriteRune(r)
		wrote += rl
		started = true
		if wrote == maxLen {
			atLimit = true
		}
	}

	return b.String(), false
}

// init registers common charset decoders for MIME parsing.
func init() {
	// Ensure common MIME types are properly mapped
	_ = mime.AddExtensionType(".eml", "message/rfc822")
}
