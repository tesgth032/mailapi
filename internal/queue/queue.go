package queue

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"mailapi/internal/model"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Queue struct {
	nc      *nats.Conn
	js      jetstream.JetStream
	stream  string
	subject string

	ackWait    time.Duration
	maxDeliver int
}

func New(ctx context.Context, url, stream, subject string, ackWait time.Duration, maxDeliver int) (*Queue, error) {
	nc, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(10),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream new: %w", err)
	}

	// Create or update the stream
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      stream,
		Subjects:  []string{subject},
		Retention: jetstream.WorkQueuePolicy,
		MaxBytes:  1 << 30, // 1GB
		MaxAge:    24 * time.Hour,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("create stream: %w", err)
	}

	if ackWait <= 0 {
		ackWait = 30 * time.Second
	}
	if maxDeliver <= 0 {
		maxDeliver = 3
	}

	return &Queue{nc: nc, js: js, stream: stream, subject: subject, ackWait: ackWait, maxDeliver: maxDeliver}, nil
}

func (q *Queue) Close() {
	q.nc.Close()
}

const (
	mailapiCodecHeader      = "X-MailAPI-Codec"
	mailapiCodecHeaderV1    = "hdr1"
	mailapiFromHeader       = "X-MailAPI-From"
	mailapiRemoteAddrHeader = "X-MailAPI-Remote-Addr"
	mailapiReceivedAtHeader = "X-MailAPI-Received-At"
	mailapiToHeader         = "X-MailAPI-To"
)

func validateIncomingEmail(email *model.IncomingEmail) error {
	if email == nil {
		return fmt.Errorf("%w: nil email", errInvalidPayload)
	}

	from := email.From
	remote := email.RemoteAddr
	to := email.To
	raw := email.RawMessage

	if len(from) > maxStringLen || len(remote) > maxStringLen {
		return fmt.Errorf("%w: string too large", errInvalidPayload)
	}
	if len(to) > maxToCount {
		return fmt.Errorf("%w: too many recipients", errInvalidPayload)
	}
	for _, r := range to {
		if len(r) > maxStringLen {
			return fmt.Errorf("%w: recipient too large", errInvalidPayload)
		}
	}
	if len(raw) > maxRawLen {
		return fmt.Errorf("%w: raw message too large", errInvalidPayload)
	}

	// NATS headers 不允许 CR/LF，避免注入。
	for _, s := range []string{from, remote} {
		if strings.IndexByte(s, '\r') != -1 || strings.IndexByte(s, '\n') != -1 {
			return fmt.Errorf("%w: invalid header value", errInvalidPayload)
		}
	}
	for _, r := range to {
		if strings.IndexByte(r, '\r') != -1 || strings.IndexByte(r, '\n') != -1 {
			return fmt.Errorf("%w: invalid recipient", errInvalidPayload)
		}
	}

	return nil
}

func (q *Queue) Publish(ctx context.Context, email *model.IncomingEmail) error {
	// 为减少大邮件在发布时的额外拷贝（尤其是 RawMessage），优先采用 “Headers + Body” 编码：
	// - NATS headers 存元数据（from/to/remote/receivedAt/codec）
	// - message body 直接使用 raw RFC822 bytes（避免把 raw 再复制进自定义封包）
	//
	// 仍保留 codec.go 的二进制/JSON 解码作为向后兼容（老消息仍可消费）。
	if err := validateIncomingEmail(email); err != nil {
		return fmt.Errorf("encode email: %w", err)
	}

	m := &nats.Msg{
		Subject: q.subject,
		Data:    email.RawMessage,
		Header:  nats.Header{},
	}
	m.Header.Set(mailapiCodecHeader, mailapiCodecHeaderV1)
	m.Header.Set(mailapiFromHeader, email.From)
	m.Header.Set(mailapiRemoteAddrHeader, email.RemoteAddr)
	m.Header.Set(mailapiReceivedAtHeader, strconv.FormatInt(email.ReceivedAt, 10))
	for _, r := range email.To {
		m.Header.Add(mailapiToHeader, r)
	}

	_, err := q.js.PublishMsg(ctx, m)
	if errors.Is(err, nats.ErrHeadersNotSupported) {
		// 极老版本 NATS 可能不支持 headers；回退到二进制封包，保证可用性。
		data, encErr := encodeIncomingEmail(email)
		if encErr != nil {
			return fmt.Errorf("encode email: %w", encErr)
		}
		_, err = q.js.Publish(ctx, q.subject, data)
	}
	return err
}

type MessageHandler func(ctx context.Context, email *model.IncomingEmail) error

func decodeIncomingEmailFromHeaders(msg jetstream.Msg, out *model.IncomingEmail) (bool, error) {
	if out == nil {
		return false, fmt.Errorf("%w: nil out", errInvalidPayload)
	}
	if msg == nil {
		return false, fmt.Errorf("%w: nil msg", errInvalidPayload)
	}

	h := msg.Headers()
	if len(h) == 0 {
		return false, nil
	}
	if strings.ToLower(strings.TrimSpace(h.Get(mailapiCodecHeader))) != mailapiCodecHeaderV1 {
		return false, nil
	}

	out.From = h.Get(mailapiFromHeader)
	out.RemoteAddr = h.Get(mailapiRemoteAddrHeader)
	out.RawMessage = msg.Data()

	if s := strings.TrimSpace(h.Get(mailapiReceivedAtHeader)); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return true, fmt.Errorf("%w: invalid receivedAt", errInvalidPayload)
		}
		out.ReceivedAt = v
	}

	// 新编码：多个 header value，每个 value 对应一个 rcpt（避免 join/split 以及逗号歧义）。
	if vv := h.Values(mailapiToHeader); len(vv) > 0 {
		if len(vv) > maxToCount {
			return true, fmt.Errorf("%w: too many recipients", errInvalidPayload)
		}
		out.To = make([]string, 0, len(vv))
		for _, p := range vv {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			out.To = append(out.To, p)
		}
	} else if s := strings.TrimSpace(h.Get(mailapiToHeader)); s != "" {
		// 兼容早期编码：单 value 以逗号拼接。
		parts := strings.Split(s, ",")
		if len(parts) > maxToCount {
			return true, fmt.Errorf("%w: too many recipients", errInvalidPayload)
		}
		out.To = make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			out.To = append(out.To, p)
		}
	}

	if err := validateIncomingEmail(out); err != nil {
		return true, err
	}
	return true, nil
}

func (q *Queue) Consume(ctx context.Context, handler MessageHandler) error {
	consumer, err := q.js.CreateOrUpdateConsumer(ctx, q.stream, jetstream.ConsumerConfig{
		Durable:       "email-worker",
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    q.maxDeliver,
		AckWait:       q.ackWait,
		FilterSubject: q.subject,
	})
	if err != nil {
		return fmt.Errorf("create consumer: %w", err)
	}

	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		handled := false
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PANIC: jetstream consume callback: %v\n%s", r, debug.Stack())
				// 尽量让消息稍后重试；幂等去重会避免重复入库。
				if !handled {
					_ = msg.NakWithDelay(5 * time.Second)
				}
			}
		}()

		var email model.IncomingEmail
		if md, err := msg.Metadata(); err == nil && md != nil {
			email.IngestStream = md.Stream
			if md.Sequence.Stream > uint64(math.MaxInt64) {
				// 极端情况下 stream seq 可能溢出 int64；此时禁用幂等字段，避免写入错误。
				email.IngestSeq = 0
			} else {
				email.IngestSeq = int64(md.Sequence.Stream)
			}
		}

		// 新编码：优先从 headers 解码，减少解码开销与分配。
		if ok, err := decodeIncomingEmailFromHeaders(msg, &email); err != nil {
			log.Printf("ERROR: decode incoming email (headers): %v", err)
			handled = true
			_ = msg.Term()
			return
		} else if !ok {
			// 向后兼容：老消息仍可能是二进制封包或 JSON（RawMessage base64）。
			if err := decodeIncomingEmail(msg.Data(), &email); err != nil {
				log.Printf("ERROR: decode incoming email: %v", err)
				handled = true
				_ = msg.Term()
				return
			}
		}

		// 长任务续租：避免处理时间超过 AckWait 导致重投递与重复入库。
		done := make(chan struct{})
		defer close(done)
		inProgressEvery := q.ackWait / 2
		if inProgressEvery > 30*time.Second {
			inProgressEvery = 30 * time.Second
		}
		if inProgressEvery < 5*time.Second {
			inProgressEvery = 5 * time.Second
		}
		go func() {
			ticker := time.NewTicker(inProgressEvery)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					_ = msg.InProgress()
				}
			}
		}()

		err := handler(ctx, &email)

		if err != nil {
			log.Printf("ERROR: process email: %v", err)
			if IsPermanent(err) {
				handled = true
				_ = msg.Term()
				return
			}

			// 对可重试错误使用延迟 NAK，避免瞬时重投递把 CPU 打爆。
			delay := time.Second
			if md, mdErr := msg.Metadata(); mdErr == nil && md != nil && md.NumDelivered > 1 {
				// 2^(n-2) 秒：第 2 次开始 1s，第 3 次 2s，第 4 次 4s...
				exp := md.NumDelivered - 2
				if exp > 6 {
					exp = 6
				}
				delay = time.Duration(1<<exp) * time.Second
			}
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			handled = true
			_ = msg.NakWithDelay(delay)
			return
		}

		handled = true
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	<-ctx.Done()
	cc.Stop()
	return nil
}
