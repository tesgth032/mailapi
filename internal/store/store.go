package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mailapi/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/sync/singleflight"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrDuplicateKey   = errors.New("duplicate key")
	ErrInvalidID      = errors.New("invalid id")
	ErrAccountDeleted = errors.New("account deleted")
)

type Store struct {
	client   *mongo.Client
	db       *mongo.Database
	domains  *mongo.Collection
	accounts *mongo.Collection
	messages *mongo.Collection

	// 用于“单 address 被高并发打爆”场景：同一时刻同一 address 的查询只打一次 Mongo。
	accountIDByAddressSF singleflight.Group

	// 用于“单 account 高频轮询 /messages 被打爆”场景：对 CountDocuments 做并发合并 + 短 TTL 缓存。
	messageCountSF        singleflight.Group
	messageCountCache     sync.Map // string(accountIDHex) -> *messageCountEntry
	messageCountLastSweep atomic.Int64

	// 用于“单 account 高频轮询 /messages 被打爆”场景：对列表查询做并发合并，减少 Mongo 往返与解码开销。
	listMessagesSF singleflight.Group
}

// MongoConfig 定义 MongoDB 连接与客户端侧调参选项。
// 注意：MaxPoolSize/MaxConnecting 支持 “0=自动，<0=不覆盖驱动默认”。
type MongoConfig struct {
	URI                    string
	Database               string
	MaxPoolSize            int64
	MinPoolSize            int64
	MaxConnecting          int64
	ConnectTimeout         time.Duration
	ServerSelectionTimeout time.Duration
	MaxConnIdleTime        time.Duration
	AppName                string
}

func New(ctx context.Context, cfg MongoConfig, accountTTL, messageTTL time.Duration) (*Store, error) {
	clientOpts := options.Client().ApplyURI(cfg.URI)

	if cfg.AppName != "" {
		clientOpts.SetAppName(cfg.AppName)
	}

	maxPool, setMaxPool := effectiveMaxPoolSize(cfg.MaxPoolSize)
	if setMaxPool {
		clientOpts.SetMaxPoolSize(uint64(maxPool))
	}

	if minPool, ok := effectiveMinPoolSize(cfg.MinPoolSize); ok {
		clientOpts.SetMinPoolSize(uint64(minPool))
	}

	if maxConn, ok := effectiveMaxConnecting(cfg.MaxConnecting, maxPool, setMaxPool); ok {
		clientOpts.SetMaxConnecting(uint64(maxConn))
	}
	if cfg.ConnectTimeout > 0 {
		clientOpts.SetConnectTimeout(cfg.ConnectTimeout)
	}
	if cfg.ServerSelectionTimeout > 0 {
		clientOpts.SetServerSelectionTimeout(cfg.ServerSelectionTimeout)
	}
	if cfg.MaxConnIdleTime > 0 {
		clientOpts.SetMaxConnIdleTime(cfg.MaxConnIdleTime)
	}

	client, err := mongo.Connect(clientOpts)
	if err != nil {
		return nil, err
	}

	if err := client.Ping(ctx, nil); err != nil {
		// 启动失败时尽量释放连接资源，避免在重试/重启场景下留下无用连接。
		discCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = client.Disconnect(discCtx)
		cancel()
		return nil, err
	}

	db := client.Database(cfg.Database)
	s := &Store{
		client:   client,
		db:       db,
		domains:  db.Collection("domains"),
		accounts: db.Collection("accounts"),
		messages: db.Collection("messages"),
	}

	if err := s.ensureIndexes(ctx, accountTTL, messageTTL); err != nil {
		discCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = client.Disconnect(discCtx)
		cancel()
		return nil, err
	}

	return s, nil
}

func effectiveMaxPoolSize(v int64) (int64, bool) {
	// <0: 不覆盖驱动默认
	if v < 0 {
		return 0, false
	}
	// >0: 固定值
	if v > 0 {
		return v, true
	}

	// 0: 自动（对高并发更友好，避免默认 100 连接在 API + Worker 场景下成为瓶颈）
	// 注意：过大的连接池会反过来压垮 Mongo，因此做上限保护。
	gomax := int64(runtime.GOMAXPROCS(0))
	auto := gomax * 32
	if auto < 100 {
		auto = 100
	}
	if auto > 300 {
		auto = 300
	}
	return auto, true
}

func effectiveMinPoolSize(v int64) (int64, bool) {
	// <0: 不覆盖驱动默认
	if v < 0 {
		return 0, false
	}
	// 0: 默认即 0，不需要显式设置
	if v == 0 {
		return 0, false
	}
	return v, true
}

func effectiveMaxConnecting(v, maxPool int64, maxPoolSet bool) (int64, bool) {
	// <0: 不覆盖驱动默认
	if v < 0 {
		return 0, false
	}

	var out int64
	if v > 0 {
		out = v
	} else {
		// 0: 自动（适当提高连接建立并发，减少突发流量下的“建连阻塞”）
		out = int64(runtime.GOMAXPROCS(0))
		if out < 4 {
			out = 4
		}
		if out > 16 {
			out = 16
		}
	}

	if maxPoolSet && maxPool > 0 && out > maxPool {
		out = maxPool
	}
	if out <= 0 {
		return 0, false
	}
	return out, true
}

func (s *Store) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

// Ping 用于 readiness 检查：验证 MongoDB 可用性（轻量，不做业务读写）。
func (s *Store) Ping(ctx context.Context) error {
	return s.client.Ping(ctx, nil)
}

func (s *Store) ensureIndexes(ctx context.Context, accountTTL, messageTTL time.Duration) error {
	// Domains: unique on domain
	if err := ensureUniqueIndex(ctx, s.domains, bson.D{{Key: "domain", Value: 1}}); err != nil {
		return err
	}
	// Domains: frequently listed by isActive
	if err := ensureIndex(ctx, s.domains, bson.D{{Key: "isActive", Value: 1}}); err != nil {
		return err
	}

	// Accounts: unique on address
	if err := ensureUniqueIndex(ctx, s.accounts, bson.D{{Key: "address", Value: 1}}); err != nil {
		return err
	}

	// Accounts: TTL index (可配置；允许在不重建索引的情况下更新 expireAfterSeconds)
	if err := ensureTTLIndex(ctx, s.db, s.accounts, "accounts", bson.D{{Key: "createdAt", Value: 1}}, accountTTL, nil); err != nil {
		return err
	}

	// Messages: compound index for list/count
	// 增加 _id 作为稳定排序的 tie-breaker，便于 cursor 分页且不依赖 skip。
	if err := ensureIndex(ctx, s.messages, bson.D{{Key: "accountId", Value: 1}, {Key: "isDeleted", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}); err != nil {
		return err
	}

	// Messages: compound index with seen filter (common query pattern: /messages?seen=true/false)
	if err := ensureIndex(ctx, s.messages, bson.D{{Key: "accountId", Value: 1}, {Key: "isDeleted", Value: 1}, {Key: "seen", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}); err != nil {
		return err
	}

	// Messages: drop legacy index without _id if present (new compound covers it).
	if err := dropIndexByKeysIfExists(ctx, s.messages, bson.D{{Key: "accountId", Value: 1}, {Key: "isDeleted", Value: 1}, {Key: "createdAt", Value: -1}}); err != nil {
		return err
	}

	// Messages: drop legacy redundant index {accountId: 1} if present (new compound covers it).
	if err := dropIndexByKeysIfExists(ctx, s.messages, bson.D{{Key: "accountId", Value: 1}}); err != nil {
		return err
	}

	// Messages: 将历史上缺失 keep 字段的文档补齐为 keep=false，确保 TTL partial index
	// 在各 MongoDB 版本上都能稳定工作（Mongo 不支持在 partial index 里使用 $ne）。
	if err := normalizeMissingMessageKeep(ctx, s.messages); err != nil {
		return err
	}

	// Messages: TTL index (可配置；允许在不重建索引的情况下更新 expireAfterSeconds)
	// keep=true 的消息不会过期（长期保留），keep=false 的消息继续走 TTL 自动过期。
	if err := ensureTTLIndex(ctx, s.db, s.messages, "messages", bson.D{{Key: "createdAt", Value: 1}}, messageTTL, messageKeepTTLPartialFilter()); err != nil {
		return err
	}

	// Messages: idempotency (JetStream ingest metadata). Unique per (accountId, ingestStream, ingestSeq).
	if err := ensureUniquePartialIndex(ctx, s.messages,
		bson.D{{Key: "accountId", Value: 1}, {Key: "ingestStream", Value: 1}, {Key: "ingestSeq", Value: 1}},
		bson.M{"ingestSeq": bson.M{"$exists": true}, "ingestStream": bson.M{"$exists": true}},
	); err != nil {
		return err
	}

	return nil
}

type indexInfo struct {
	Name               string `bson:"name"`
	Key                bson.D `bson:"key"`
	Unique             bool   `bson:"unique,omitempty"`
	ExpireAfterSeconds any    `bson:"expireAfterSeconds,omitempty"`
	PartialFilter      bson.M `bson:"partialFilterExpression,omitempty"`
}

func listIndexes(ctx context.Context, coll *mongo.Collection) ([]indexInfo, error) {
	cur, err := coll.Indexes().List(ctx)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var out []indexInfo
	for cur.Next(ctx) {
		var ii indexInfo
		if err := cur.Decode(&ii); err != nil {
			return nil, err
		}
		out = append(out, ii)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		// 某些解码路径可能把数字转成 float64（例如中间被 map[string]any 承接过）。
		return int64(t), true
	default:
		return 0, false
	}
}

func keysEqual(a, b bson.D) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key {
			return false
		}
		av, aok := asInt64(a[i].Value)
		bv, bok := asInt64(b[i].Value)
		if aok && bok {
			if av != bv {
				return false
			}
			continue
		}
		if a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

func findIndexByKeys(idxs []indexInfo, keys bson.D) *indexInfo {
	for i := range idxs {
		if keysEqual(idxs[i].Key, keys) {
			return &idxs[i]
		}
	}
	return nil
}

func isIndexNotFoundError(err error) bool {
	var ce mongo.CommandError
	if errors.As(err, &ce) {
		// MongoDB: IndexNotFound
		if ce.Code == 27 {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "index not found") || strings.Contains(msg, "no such index")
}

func dropIndexIgnoreNotFound(ctx context.Context, coll *mongo.Collection, name string) error {
	if name == "" {
		return nil
	}
	if err := coll.Indexes().DropOne(ctx, name); err != nil {
		if isIndexNotFoundError(err) {
			return nil
		}
		return err
	}
	return nil
}

func ensureUniqueIndex(ctx context.Context, coll *mongo.Collection, keys bson.D) error {
	idxs, err := listIndexes(ctx, coll)
	if err != nil {
		return err
	}

	idx := findIndexByKeys(idxs, keys)
	if idx != nil {
		if idx.Unique {
			return nil
		}
		// 同键但非唯一：会导致 FindOne 的“假唯一”以及未来加唯一失败；直接替换为唯一索引。
		if err := dropIndexIgnoreNotFound(ctx, coll, idx.Name); err != nil {
			return err
		}
	}

	_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    keys,
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		// 可能是并发启动（API + Worker）导致另一个进程已创建；这里复查一次，若已满足则视为成功。
		idxs2, listErr := listIndexes(ctx, coll)
		if listErr == nil {
			if idx2 := findIndexByKeys(idxs2, keys); idx2 != nil && idx2.Unique {
				return nil
			}
		}
		return err
	}
	return nil
}

func ensureUniquePartialIndex(ctx context.Context, coll *mongo.Collection, keys bson.D, partial bson.M) error {
	idxs, err := listIndexes(ctx, coll)
	if err != nil {
		return err
	}

	idx := findIndexByKeys(idxs, keys)
	if idx != nil {
		if idx.Unique && partialFilterEqual(idx.PartialFilter, partial) {
			return nil
		}
		// 同键但选项不一致：替换为“唯一 + partial”索引。
		if err := dropIndexIgnoreNotFound(ctx, coll, idx.Name); err != nil {
			return err
		}
	}

	_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: keys,
		Options: options.Index().
			SetUnique(true).
			SetPartialFilterExpression(partial),
	})
	if err != nil {
		// 并发启动下可能已被创建；复查确认即可。
		idxs2, listErr := listIndexes(ctx, coll)
		if listErr == nil {
			if idx2 := findIndexByKeys(idxs2, keys); idx2 != nil && idx2.Unique && partialFilterEqual(idx2.PartialFilter, partial) {
				return nil
			}
		}
		return err
	}
	return nil
}

func ensureIndex(ctx context.Context, coll *mongo.Collection, keys bson.D) error {
	idxs, err := listIndexes(ctx, coll)
	if err != nil {
		return err
	}
	if findIndexByKeys(idxs, keys) != nil {
		return nil
	}

	_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: keys})
	if err != nil {
		// 并发启动下可能已被创建；复查确认即可。
		idxs2, listErr := listIndexes(ctx, coll)
		if listErr == nil {
			if findIndexByKeys(idxs2, keys) != nil {
				return nil
			}
		}
		return err
	}
	return nil
}

func ttlSeconds(ttl time.Duration) (int64, bool) {
	if ttl <= 0 {
		return 0, false
	}
	secs := int64(ttl / time.Second)
	if ttl%time.Second != 0 {
		// TTL 只能配置“秒”，这里向上取整，避免把配置的 TTL 截断变短。
		secs++
	}
	if secs < 1 {
		secs = 1
	}
	return secs, true
}

func partialFilterEqual(a, b bson.M) bool {
	// nil 与空 map 视为等价
	if len(a) == 0 {
		a = nil
	}
	if len(b) == 0 {
		b = nil
	}
	return reflect.DeepEqual(a, b)
}

func messageKeepTTLPartialFilter() bson.M {
	return bson.M{"keep": false}
}

func normalizeMissingMessageKeep(ctx context.Context, coll *mongo.Collection) error {
	_, err := coll.UpdateMany(
		ctx,
		bson.M{"keep": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"keep": false}},
	)
	return err
}

func ensureTTLIndex(ctx context.Context, db *mongo.Database, coll *mongo.Collection, collName string, keys bson.D, ttl time.Duration, partial bson.M) error {
	idxs, err := listIndexes(ctx, coll)
	if err != nil {
		return err
	}
	idx := findIndexByKeys(idxs, keys)

	desiredSecs, enabled := ttlSeconds(ttl)
	if !enabled {
		// 禁用 TTL：仅在“确实是 TTL 索引”时才删除，避免误删用户自建的普通索引。
		if idx != nil && idx.ExpireAfterSeconds != nil {
			return dropIndexIgnoreNotFound(ctx, coll, idx.Name)
		}
		return nil
	}

	const maxInt32 = int64(2147483647)
	if desiredSecs > maxInt32 {
		return fmt.Errorf("%s TTL too large: %d seconds", collName, desiredSecs)
	}

	if idx != nil {
		if idx.ExpireAfterSeconds == nil {
			return fmt.Errorf("%s TTL index conflict: existing index %q uses keys %v but is not TTL", collName, idx.Name, keys)
		}

		// partialFilterExpression 不可通过 collMod 修改；不一致时必须 drop + recreate。
		if !partialFilterEqual(idx.PartialFilter, partial) {
			if err := dropIndexIgnoreNotFound(ctx, coll, idx.Name); err != nil {
				return err
			}
			idx = nil
		}
	}

	if idx != nil {
		if curSecs, ok := asInt64(idx.ExpireAfterSeconds); ok && curSecs == desiredSecs {
			return nil
		}

		// 尽量使用 collMod 更新 TTL（更快，且避免重建索引）。若失败再回退 drop + create。
		cmd := bson.D{
			{Key: "collMod", Value: collName},
			{Key: "index", Value: bson.D{
				{Key: "name", Value: idx.Name},
				{Key: "expireAfterSeconds", Value: desiredSecs},
			}},
		}
		if err := db.RunCommand(ctx, cmd).Err(); err == nil {
			return nil
		}

		if err := dropIndexIgnoreNotFound(ctx, coll, idx.Name); err != nil {
			return err
		}
	}

	createOpts := options.Index().SetExpireAfterSeconds(int32(desiredSecs))
	if len(partial) > 0 {
		createOpts.SetPartialFilterExpression(partial)
	}
	_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    keys,
		Options: createOpts,
	})
	if err != nil {
		// 并发启动下可能已被创建/更新；复查确认即可。
		idxs2, listErr := listIndexes(ctx, coll)
		if listErr == nil {
			if idx2 := findIndexByKeys(idxs2, keys); idx2 != nil && idx2.ExpireAfterSeconds != nil {
				if curSecs, ok := asInt64(idx2.ExpireAfterSeconds); ok && curSecs == desiredSecs {
					if partialFilterEqual(idx2.PartialFilter, partial) {
						return nil
					}
					// TTL 匹配但 partial 不匹配：需要替换。
					_ = dropIndexIgnoreNotFound(ctx, coll, idx2.Name)
				}
			}
		}
		return err
	}
	return nil
}

func dropIndexByKeysIfExists(ctx context.Context, coll *mongo.Collection, keys bson.D) error {
	idxs, err := listIndexes(ctx, coll)
	if err != nil {
		return err
	}
	idx := findIndexByKeys(idxs, keys)
	if idx == nil {
		return nil
	}
	// 安全起见：永远不动 _id_ 索引
	if idx.Name == "_id_" {
		return nil
	}
	return dropIndexIgnoreNotFound(ctx, coll, idx.Name)
}

func isDuplicateKeyError(err error) bool {
	var we mongo.WriteException
	if errors.As(err, &we) {
		for _, e := range we.WriteErrors {
			if e.Code == 11000 {
				return true
			}
		}
	}
	return false
}

// --- Domain Operations ---

func (s *Store) ListDomains(ctx context.Context) ([]model.Domain, error) {
	filter := bson.M{"isActive": true}
	cursor, err := s.domains.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var domains []model.Domain
	if err := cursor.All(ctx, &domains); err != nil {
		return nil, err
	}
	if domains == nil {
		domains = []model.Domain{}
	}
	return domains, nil
}

func (s *Store) GetDomainByName(ctx context.Context, domain string) (*model.Domain, error) {
	var d model.Domain
	err := s.domains.FindOne(ctx, bson.M{"domain": domain, "isActive": true}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &d, err
}

func (s *Store) CreateDomain(ctx context.Context, domain *model.Domain) error {
	domain.CreatedAt = time.Now()
	domain.UpdatedAt = domain.CreatedAt
	_, err := s.domains.InsertOne(ctx, domain)
	if isDuplicateKeyError(err) {
		return ErrDuplicateKey
	}
	return err
}

// SyncDomains upserts domains from config into MongoDB.
// Existing domains are updated; new domains are created. Domains not in the
// list are left untouched (they may have been added manually).
func (s *Store) SyncDomains(ctx context.Context, domains []model.Domain) error {
	for _, d := range domains {
		filter := bson.M{"domain": d.Domain}
		update := bson.M{
			"$set": bson.M{
				"domain":    d.Domain,
				"isActive":  d.IsActive,
				"isPrivate": d.IsPrivate,
				"updatedAt": time.Now(),
			},
			"$setOnInsert": bson.M{
				"createdAt": time.Now(),
			},
		}
		opts := options.UpdateOne().SetUpsert(true)
		if _, err := s.domains.UpdateOne(ctx, filter, update, opts); err != nil {
			return fmt.Errorf("sync domain %s: %w", d.Domain, err)
		}
	}
	return nil
}

// --- Account Operations ---

func (s *Store) CreateAccount(ctx context.Context, account *model.Account) error {
	account.CreatedAt = time.Now()
	account.UpdatedAt = account.CreatedAt
	if account.Quota == 0 {
		account.Quota = 40000000 // ~40MB default quota
	}
	res, err := s.accounts.InsertOne(ctx, account)
	if isDuplicateKeyError(err) {
		return ErrDuplicateKey
	}
	// 确保响应里包含新建账号的 id（有些编码路径不会回写 struct 的 _id）。
	if err == nil && res != nil {
		if oid, ok := res.InsertedID.(bson.ObjectID); ok {
			account.ID = oid
		}
	}
	return err
}

func (s *Store) GetAccount(ctx context.Context, id string) (*model.Account, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, ErrInvalidID
	}
	var account model.Account
	err = s.accounts.FindOne(ctx, bson.M{"_id": oid}).Decode(&account)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &account, err
}

// GetAccountIDByAddress 仅返回 account 的 _id，避免在 Worker 高并发路径中反复解码 password/quota 等无关字段。
func (s *Store) GetAccountIDByAddress(ctx context.Context, address string) (bson.ObjectID, error) {
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" {
		return bson.ObjectID{}, ErrNotFound
	}

	// 同一时刻同一 address 的查询只打一次 Mongo，专门优化“单 key 高并发”热点。
	// 注意：用 DoChan 允许调用方按 ctx 取消等待；同时避免“第一个调用的 ctx 取消导致整组都失败”。
	ch := s.accountIDByAddressSF.DoChan(address, func() (any, error) {
		var r struct {
			ID bson.ObjectID `bson:"_id"`
		}
		opts := options.FindOne().SetProjection(bson.M{"_id": 1})
		// 用后台 ctx 执行实际查询，避免某个等待方 ctx 取消导致共享查询被中止。
		// 同时加一个上限超时，避免 Mongo 异常时查询无限挂起。
		qCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		qErr := s.accounts.FindOne(qCtx, bson.M{"address": address}, opts).Decode(&r)
		if errors.Is(qErr, mongo.ErrNoDocuments) {
			return bson.ObjectID{}, ErrNotFound
		}
		if qErr != nil {
			return bson.ObjectID{}, qErr
		}
		return r.ID, nil
	})

	select {
	case <-ctx.Done():
		return bson.ObjectID{}, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return bson.ObjectID{}, res.Err
		}
		id, ok := res.Val.(bson.ObjectID)
		if !ok {
			return bson.ObjectID{}, fmt.Errorf("unexpected result type %T", res.Val)
		}
		return id, nil
	}
}

func (s *Store) GetAccountByAddress(ctx context.Context, address string) (*model.Account, error) {
	var account model.Account
	err := s.accounts.FindOne(ctx, bson.M{"address": address}).Decode(&account)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &account, err
}

func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return ErrInvalidID
	}
	result, err := s.accounts.DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateAccountUsed(ctx context.Context, id bson.ObjectID, delta int64) error {
	// used 允许在异常/回滚时做负向修正，但必须保证最终不小于 0。
	// 这里使用 update pipeline（MongoDB 4.2+）实现 $max(0, used+delta) 的原子更新。
	now := time.Now()

	// delta==0 仅更新时间戳，避免 pipeline 带来的额外开销。
	if delta == 0 {
		_, err := s.accounts.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"updatedAt": now}})
		return err
	}

	update := mongo.Pipeline{
		bson.D{{Key: "$set", Value: bson.M{
			"used": bson.M{
				"$max": []any{
					int64(0),
					bson.M{"$add": []any{
						bson.M{"$ifNull": []any{"$used", int64(0)}},
						delta,
					}},
				},
			},
			"updatedAt": now,
		}}},
	}

	_, err := s.accounts.UpdateOne(ctx, bson.M{"_id": id}, update)
	return err
}

func (s *Store) TryReserveAccountUsed(ctx context.Context, id bson.ObjectID, delta int64) (bool, error) {
	if delta <= 0 {
		return true, nil
	}

	now := time.Now()
	filter := bson.M{
		"_id": id,
		"$or": []bson.M{
			// quota<=0 视为不限额
			{"quota": bson.M{"$lte": 0}},
			// used+delta <= quota
			{"$expr": bson.M{"$lte": []any{
				bson.M{"$add": []any{
					bson.M{"$ifNull": []any{"$used", int64(0)}},
					delta,
				}},
				"$quota",
			}}},
		},
	}
	update := bson.M{
		"$inc": bson.M{"used": delta},
		"$set": bson.M{"updatedAt": now},
	}

	res, err := s.accounts.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

func (s *Store) RecalculateAccountUsed(ctx context.Context, id bson.ObjectID) (int64, error) {
	// 只统计未软删除消息；用于 TTL 自动删除/异常回滚导致 used 漂移的纠偏。
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{"accountId": id, "isDeleted": false}}},
		bson.D{{Key: "$group", Value: bson.M{"_id": nil, "used": bson.M{"$sum": bson.M{"$ifNull": []any{"$size", int64(0)}}}}}},
	}

	cur, err := s.messages.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)

	var out struct {
		Used int64 `bson:"used"`
	}
	used := int64(0)
	if cur.Next(ctx) {
		if err := cur.Decode(&out); err != nil {
			return 0, err
		}
		used = out.Used
	}
	if err := cur.Err(); err != nil {
		return 0, err
	}
	if used < 0 {
		used = 0
	}

	// 写回 accounts.used（如果账号不存在，MatchedCount=0；调用方可按需处理）
	_, err = s.accounts.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"used": used, "updatedAt": time.Now()}})
	if err != nil {
		return 0, err
	}
	return used, nil
}

// --- Message Operations ---

func (s *Store) CreateMessage(ctx context.Context, msg *model.Message) error {
	now := time.Now()
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = now
	}
	// 创建时 UpdatedAt 至少不应早于 CreatedAt。
	if msg.UpdatedAt.IsZero() || msg.UpdatedAt.Before(msg.CreatedAt) {
		msg.UpdatedAt = msg.CreatedAt
	}
	_, err := s.messages.InsertOne(ctx, msg)
	if isDuplicateKeyError(err) {
		return ErrDuplicateKey
	}
	return err
}

func (s *Store) GetMessage(ctx context.Context, id string) (*model.Message, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, ErrInvalidID
	}
	var msg model.Message
	// 默认不加载 rawMessage，避免 API 高频读取消息时把 Mongo 大字段带出来导致 CPU/IO 飙升。
	opts := options.FindOne().SetProjection(bson.M{"rawMessage": 0})
	err = s.messages.FindOne(ctx, bson.M{"_id": oid, "isDeleted": false}, opts).Decode(&msg)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &msg, err
}

func (s *Store) GetMessageMeta(ctx context.Context, id string) (*model.Message, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, ErrInvalidID
	}
	var msg model.Message
	opts := options.FindOne().SetProjection(bson.M{"rawMessage": 0, "text": 0, "html": 0})
	err = s.messages.FindOne(ctx, bson.M{"_id": oid, "isDeleted": false}, opts).Decode(&msg)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &msg, err
}

func (s *Store) GetMessageRaw(ctx context.Context, id string) (*model.Message, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, ErrInvalidID
	}
	var msg model.Message
	// 只取下载所需字段：accountId + rawMessage（_id 默认会包含）。
	opts := options.FindOne().SetProjection(bson.M{"accountId": 1, "rawMessage": 1})
	err = s.messages.FindOne(ctx, bson.M{"_id": oid, "isDeleted": false}, opts).Decode(&msg)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return &msg, err
}

func (s *Store) HasMessage(ctx context.Context, id string) (bool, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return false, ErrInvalidID
	}

	// 轻量存在性检查：只取 _id。
	var out struct {
		ID bson.ObjectID `bson:"_id"`
	}
	opts := options.FindOne().SetProjection(bson.M{"_id": 1})
	err = s.messages.FindOne(ctx, bson.M{"_id": oid, "isDeleted": false}, opts).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) HasMessageByIngest(ctx context.Context, accountID bson.ObjectID, ingestStream string, ingestSeq int64) (bool, error) {
	ingestStream = strings.TrimSpace(ingestStream)
	if ingestStream == "" || ingestSeq <= 0 {
		return false, nil
	}

	filter := bson.M{
		"accountId":    accountID,
		"ingestStream": ingestStream,
		"ingestSeq":    ingestSeq,
	}
	var out struct {
		ID bson.ObjectID `bson:"_id"`
	}
	opts := options.FindOne().SetProjection(bson.M{"_id": 1})
	err := s.messages.FindOne(ctx, filter, opts).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ExistingMessageIDs(ctx context.Context, ids []string) (map[string]struct{}, error) {
	if len(ids) == 0 {
		return map[string]struct{}{}, nil
	}

	oids := make([]bson.ObjectID, 0, len(ids))
	for _, id := range ids {
		oid, err := bson.ObjectIDFromHex(strings.TrimSpace(id))
		if err != nil {
			continue
		}
		oids = append(oids, oid)
	}
	if len(oids) == 0 {
		return map[string]struct{}{}, nil
	}

	filter := bson.M{"_id": bson.M{"$in": oids}, "isDeleted": false}
	opts := options.Find().SetProjection(bson.M{"_id": 1})
	cur, err := s.messages.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	out := make(map[string]struct{}, len(oids))
	for cur.Next(ctx) {
		var r struct {
			ID bson.ObjectID `bson:"_id"`
		}
		if err := cur.Decode(&r); err != nil {
			return nil, err
		}
		out[r.ID.Hex()] = struct{}{}
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ListMessages(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error) {
	type result struct {
		messages []model.Message
		total    int64
	}

	key := accountID.Hex() + ":p:" + strconv.Itoa(page) + ":n:" + strconv.Itoa(perPage)
	ch := s.listMessagesSF.DoChan(key, func() (any, error) {
		qCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		total, err := s.countMessagesCached(qCtx, accountID)
		if err != nil {
			return nil, err
		}

		filter := bson.M{"accountId": accountID, "isDeleted": false}
		skip := int64((page - 1) * perPage)
		opts := options.Find().
			SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).
			SetSkip(skip).
			SetLimit(int64(perPage)).
			SetProjection(bson.M{"rawMessage": 0, "text": 0, "html": 0})

		cursor, err := s.messages.Find(qCtx, filter, opts)
		if err != nil {
			return nil, err
		}
		defer cursor.Close(qCtx)

		var messages []model.Message
		if err := cursor.All(qCtx, &messages); err != nil {
			return nil, err
		}
		if messages == nil {
			messages = []model.Message{}
		}
		return &result{messages: messages, total: total}, nil
	})

	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, 0, res.Err
		}
		r, ok := res.Val.(*result)
		if !ok || r == nil {
			return nil, 0, fmt.Errorf("unexpected list result type %T", res.Val)
		}
		out := append([]model.Message(nil), r.messages...) // 每个请求返回自己的切片，避免上层写字段产生数据竞争
		if out == nil {
			out = []model.Message{}
		}
		return out, r.total, nil
	}
}

func (s *Store) ListMessagesFiltered(ctx context.Context, accountID bson.ObjectID, page, perPage int, seen *bool) ([]model.Message, int64, error) {
	if seen == nil {
		return s.ListMessages(ctx, accountID, page, perPage)
	}

	type result struct {
		messages []model.Message
		total    int64
	}

	key := accountID.Hex() + ":seen:" + strconv.FormatBool(*seen) + ":p:" + strconv.Itoa(page) + ":n:" + strconv.Itoa(perPage)
	ch := s.listMessagesSF.DoChan(key, func() (any, error) {
		qCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		total, err := s.countMessagesCachedWithFilter(qCtx, accountID, seen)
		if err != nil {
			return nil, err
		}

		filter := bson.M{"accountId": accountID, "isDeleted": false, "seen": *seen}
		skip := int64((page - 1) * perPage)
		opts := options.Find().
			SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).
			SetSkip(skip).
			SetLimit(int64(perPage)).
			SetProjection(bson.M{"rawMessage": 0, "text": 0, "html": 0})

		cursor, err := s.messages.Find(qCtx, filter, opts)
		if err != nil {
			return nil, err
		}
		defer cursor.Close(qCtx)

		var messages []model.Message
		if err := cursor.All(qCtx, &messages); err != nil {
			return nil, err
		}
		if messages == nil {
			messages = []model.Message{}
		}
		return &result{messages: messages, total: total}, nil
	})

	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, 0, res.Err
		}
		r, ok := res.Val.(*result)
		if !ok || r == nil {
			return nil, 0, fmt.Errorf("unexpected list result type %T", res.Val)
		}
		out := append([]model.Message(nil), r.messages...)
		if out == nil {
			out = []model.Message{}
		}
		return out, r.total, nil
	}
}

func (s *Store) ListMessagesAfter(ctx context.Context, accountID bson.ObjectID, cursorID string, limit int) ([]model.Message, int64, error) {
	type result struct {
		messages []model.Message
		total    int64
	}

	cursorID = strings.TrimSpace(cursorID)
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}

	key := accountID.Hex() + ":c:" + cursorID + ":l:" + strconv.Itoa(limit)
	ch := s.listMessagesSF.DoChan(key, func() (any, error) {
		qCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		total, err := s.countMessagesCached(qCtx, accountID)
		if err != nil {
			return nil, err
		}

		filter := bson.M{"accountId": accountID, "isDeleted": false}
		if cursorID != "" {
			// 新 cursor（推荐）："<unix_ms>.<objectIdHex>"
			// 优点：避免额外的“cursor -> createdAt”查询，分页稳定且更适合高并发。
			if ts, coid, ok := parseCursorToken(cursorID); ok {
				filter = bson.M{
					"accountId": accountID,
					"isDeleted": false,
					"$or": bson.A{
						bson.M{"createdAt": bson.M{"$lt": ts}},
						bson.M{"createdAt": ts, "_id": bson.M{"$lt": coid}},
					},
				}
			} else {
				// 旧 cursor（兼容）：直接使用 messageId，需额外查询其 createdAt。
				coid, err := bson.ObjectIDFromHex(cursorID)
				if err != nil {
					return nil, ErrInvalidID
				}

				// cursor 必须属于当前 account 且未软删除，否则视为无效 cursor。
				var cur struct {
					CreatedAt time.Time `bson:"createdAt"`
				}
				curOpts := options.FindOne().SetProjection(bson.M{"createdAt": 1})
				err = s.messages.FindOne(qCtx, bson.M{"_id": coid, "accountId": accountID, "isDeleted": false}, curOpts).Decode(&cur)
				if errors.Is(err, mongo.ErrNoDocuments) {
					return nil, ErrNotFound
				}
				if err != nil {
					return nil, err
				}

				filter = bson.M{
					"accountId": accountID,
					"isDeleted": false,
					"$or": bson.A{
						bson.M{"createdAt": bson.M{"$lt": cur.CreatedAt}},
						bson.M{"createdAt": cur.CreatedAt, "_id": bson.M{"$lt": coid}},
					},
				}
			}
		}

		opts := options.Find().
			SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).
			SetLimit(int64(limit)).
			SetProjection(bson.M{"rawMessage": 0, "text": 0, "html": 0})

		cursor, err := s.messages.Find(qCtx, filter, opts)
		if err != nil {
			return nil, err
		}
		defer cursor.Close(qCtx)

		var messages []model.Message
		if err := cursor.All(qCtx, &messages); err != nil {
			return nil, err
		}
		if messages == nil {
			messages = []model.Message{}
		}
		return &result{messages: messages, total: total}, nil
	})

	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, 0, res.Err
		}
		r, ok := res.Val.(*result)
		if !ok || r == nil {
			return nil, 0, fmt.Errorf("unexpected list result type %T", res.Val)
		}
		out := append([]model.Message(nil), r.messages...)
		if out == nil {
			out = []model.Message{}
		}
		return out, r.total, nil
	}
}

func (s *Store) ListMessagesAfterFiltered(ctx context.Context, accountID bson.ObjectID, cursorID string, limit int, seen *bool) ([]model.Message, int64, error) {
	if seen == nil {
		return s.ListMessagesAfter(ctx, accountID, cursorID, limit)
	}

	type result struct {
		messages []model.Message
		total    int64
	}

	cursorID = strings.TrimSpace(cursorID)
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}

	key := accountID.Hex() + ":seen:" + strconv.FormatBool(*seen) + ":c:" + cursorID + ":l:" + strconv.Itoa(limit)
	ch := s.listMessagesSF.DoChan(key, func() (any, error) {
		qCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		total, err := s.countMessagesCachedWithFilter(qCtx, accountID, seen)
		if err != nil {
			return nil, err
		}

		filter := bson.M{"accountId": accountID, "isDeleted": false, "seen": *seen}
		if cursorID != "" {
			if ts, coid, ok := parseCursorToken(cursorID); ok {
				filter = bson.M{
					"accountId": accountID,
					"isDeleted": false,
					"seen":      *seen,
					"$or": bson.A{
						bson.M{"createdAt": bson.M{"$lt": ts}},
						bson.M{"createdAt": ts, "_id": bson.M{"$lt": coid}},
					},
				}
			} else {
				coid, err := bson.ObjectIDFromHex(cursorID)
				if err != nil {
					return nil, ErrInvalidID
				}

				var cur struct {
					CreatedAt time.Time `bson:"createdAt"`
				}
				curOpts := options.FindOne().SetProjection(bson.M{"createdAt": 1})
				err = s.messages.FindOne(qCtx, bson.M{"_id": coid, "accountId": accountID, "isDeleted": false, "seen": *seen}, curOpts).Decode(&cur)
				if errors.Is(err, mongo.ErrNoDocuments) {
					return nil, ErrNotFound
				}
				if err != nil {
					return nil, err
				}

				filter = bson.M{
					"accountId": accountID,
					"isDeleted": false,
					"seen":      *seen,
					"$or": bson.A{
						bson.M{"createdAt": bson.M{"$lt": cur.CreatedAt}},
						bson.M{"createdAt": cur.CreatedAt, "_id": bson.M{"$lt": coid}},
					},
				}
			}
		}

		opts := options.Find().
			SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).
			SetLimit(int64(limit)).
			SetProjection(bson.M{"rawMessage": 0, "text": 0, "html": 0})

		cursor, err := s.messages.Find(qCtx, filter, opts)
		if err != nil {
			return nil, err
		}
		defer cursor.Close(qCtx)

		var messages []model.Message
		if err := cursor.All(qCtx, &messages); err != nil {
			return nil, err
		}
		if messages == nil {
			messages = []model.Message{}
		}
		return &result{messages: messages, total: total}, nil
	})

	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, 0, res.Err
		}
		r, ok := res.Val.(*result)
		if !ok || r == nil {
			return nil, 0, fmt.Errorf("unexpected list result type %T", res.Val)
		}
		out := append([]model.Message(nil), r.messages...)
		if out == nil {
			out = []model.Message{}
		}
		return out, r.total, nil
	}
}

// parseCursorToken parses "<unix_ms>.<objectIdHex>".
// ok=false 表示不是该格式（将回退到旧 cursor 逻辑）。
func parseCursorToken(s string) (ts time.Time, id bson.ObjectID, ok bool) {
	// 快速路径：必须包含一个 '.' 分隔符
	i := strings.IndexByte(s, '.')
	if i <= 0 || i >= len(s)-1 {
		return time.Time{}, bson.ObjectID{}, false
	}

	ms, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil {
		return time.Time{}, bson.ObjectID{}, false
	}

	oid, err := bson.ObjectIDFromHex(s[i+1:])
	if err != nil {
		return time.Time{}, bson.ObjectID{}, false
	}

	return time.UnixMilli(ms).UTC(), oid, true
}

func (s *Store) UpdateMessageSeen(ctx context.Context, id string, seen bool) error {
	return s.UpdateMessageFlags(ctx, id, &seen, nil)
}

func (s *Store) UpdateMessageKeep(ctx context.Context, id string, keep bool) error {
	return s.UpdateMessageFlags(ctx, id, nil, &keep)
}

func (s *Store) UpdateMessageFlags(ctx context.Context, id string, seen, keep *bool) error {
	if seen == nil && keep == nil {
		return nil
	}

	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return ErrInvalidID
	}

	set := bson.M{
		"updatedAt": time.Now(),
	}
	if seen != nil {
		set["seen"] = *seen
	}
	if keep != nil {
		set["keep"] = *keep
	}

	result, err := s.messages.UpdateOne(ctx, bson.M{"_id": oid, "isDeleted": false}, bson.M{
		"$set": set,
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) BulkUpdateMessageFlagsByIDs(ctx context.Context, accountID bson.ObjectID, ids []string, seen, keep *bool) (int64, error) {
	if seen == nil && keep == nil {
		return 0, nil
	}
	if len(ids) == 0 {
		return 0, nil
	}

	oids := make([]bson.ObjectID, 0, len(ids))
	for _, id := range ids {
		oid, err := bson.ObjectIDFromHex(strings.TrimSpace(id))
		if err != nil {
			return 0, ErrInvalidID
		}
		oids = append(oids, oid)
	}

	set := bson.M{"updatedAt": time.Now()}
	if seen != nil {
		set["seen"] = *seen
	}
	if keep != nil {
		set["keep"] = *keep
	}

	filter := bson.M{"_id": bson.M{"$in": oids}, "accountId": accountID, "isDeleted": false}
	res, err := s.messages.UpdateMany(ctx, filter, bson.M{"$set": set})
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}

func (s *Store) BulkUpdateMessageFlagsByAccount(ctx context.Context, accountID bson.ObjectID, seen, keep *bool) (int64, error) {
	if seen == nil && keep == nil {
		return 0, nil
	}

	set := bson.M{"updatedAt": time.Now()}
	filter := bson.M{"accountId": accountID, "isDeleted": false}

	// 避免无意义写放大：仅更新“确实需要变更”的文档。
	if seen != nil {
		set["seen"] = *seen
		filter["seen"] = bson.M{"$ne": *seen}
	}
	if keep != nil {
		set["keep"] = *keep
		filter["keep"] = bson.M{"$ne": *keep}
	}

	res, err := s.messages.UpdateMany(ctx, filter, bson.M{"$set": set})
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}

func (s *Store) DeleteMessage(ctx context.Context, id string) error {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return ErrInvalidID
	}
	result, err := s.messages.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{"isDeleted": true, "updatedAt": time.Now()},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SoftDeleteMessagesByAccount(ctx context.Context, accountID bson.ObjectID, seen *bool, limit int) ([]string, int64, error) {
	if limit <= 0 {
		limit = 5000
	}
	if limit > 20000 {
		limit = 20000
	}

	filter := bson.M{"accountId": accountID, "isDeleted": false}
	if seen != nil {
		filter["seen"] = *seen
	}

	type meta struct {
		ID   bson.ObjectID `bson:"_id"`
		Size int64         `bson:"size"`
	}

	capHint := limit
	if capHint > 4096 {
		capHint = 4096
	}
	deleted := make([]string, 0, capHint)
	var totalSize int64

	remaining := limit
	for remaining > 0 {
		batch := 1000
		if batch > remaining {
			batch = remaining
		}

		opts := options.Find().
			SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).
			SetLimit(int64(batch)).
			SetProjection(bson.M{"_id": 1, "size": 1})

		cur, err := s.messages.Find(ctx, filter, opts)
		if err != nil {
			return nil, 0, err
		}

		var docs []meta
		if err := cur.All(ctx, &docs); err != nil {
			_ = cur.Close(ctx)
			return nil, 0, err
		}
		_ = cur.Close(ctx)

		if len(docs) == 0 {
			break
		}

		oids := make([]bson.ObjectID, 0, len(docs))
		for _, d := range docs {
			oids = append(oids, d.ID)
			deleted = append(deleted, d.ID.Hex())
			totalSize += d.Size
		}

		updFilter := bson.M{"_id": bson.M{"$in": oids}, "accountId": accountID, "isDeleted": false}
		_, err = s.messages.UpdateMany(ctx, updFilter, bson.M{"$set": bson.M{"isDeleted": true, "updatedAt": time.Now()}})
		if err != nil {
			return nil, 0, err
		}

		remaining -= len(docs)
	}

	if deleted == nil {
		deleted = []string{}
	}
	return deleted, totalSize, nil
}

func (s *Store) SoftDeleteMessagesByIDs(ctx context.Context, accountID bson.ObjectID, ids []string) ([]string, int64, error) {
	if len(ids) == 0 {
		return []string{}, 0, nil
	}

	oids := make([]bson.ObjectID, 0, len(ids))
	for _, id := range ids {
		oid, err := bson.ObjectIDFromHex(strings.TrimSpace(id))
		if err != nil {
			return nil, 0, ErrInvalidID
		}
		oids = append(oids, oid)
	}

	type meta struct {
		ID   bson.ObjectID `bson:"_id"`
		Size int64         `bson:"size"`
	}

	filter := bson.M{"_id": bson.M{"$in": oids}, "accountId": accountID, "isDeleted": false}
	opts := options.Find().SetProjection(bson.M{"_id": 1, "size": 1})
	cur, err := s.messages.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(ctx)

	var docs []meta
	if err := cur.All(ctx, &docs); err != nil {
		return nil, 0, err
	}
	if len(docs) == 0 {
		return []string{}, 0, nil
	}

	found := make([]bson.ObjectID, 0, len(docs))
	deleted := make([]string, 0, len(docs))
	var totalSize int64
	for _, d := range docs {
		found = append(found, d.ID)
		deleted = append(deleted, d.ID.Hex())
		totalSize += d.Size
	}

	updFilter := bson.M{"_id": bson.M{"$in": found}, "accountId": accountID, "isDeleted": false}
	_, err = s.messages.UpdateMany(ctx, updFilter, bson.M{"$set": bson.M{"isDeleted": true, "updatedAt": time.Now()}})
	if err != nil {
		return nil, 0, err
	}

	return deleted, totalSize, nil
}

func (s *Store) HardDeleteMessagesByAccount(ctx context.Context, accountID bson.ObjectID) ([]model.Message, error) {
	// Fetch messages first to get attachment info for cleanup
	// 仅需 _id + hasAttachments，用投影避免把 raw/text/html 等大字段拉出来。
	opts := options.Find().SetProjection(bson.M{"hasAttachments": 1})
	cursor, err := s.messages.Find(ctx, bson.M{"accountId": accountID}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var messages []model.Message
	if err := cursor.All(ctx, &messages); err != nil {
		return nil, err
	}

	_, err = s.messages.DeleteMany(ctx, bson.M{"accountId": accountID})
	return messages, err
}

func (s *Store) CountMessagesByAccount(ctx context.Context, accountID bson.ObjectID) (int64, error) {
	return s.countMessagesCached(ctx, accountID)
}

type messageCountEntry struct {
	count     int64
	expiresAt int64 // unix nano
}

const (
	// messageCountCacheTTL 用极短 TTL 做“削峰”：避免 /messages 高频轮询场景下每个请求都做 CountDocuments。
	// TTL 越短，totalItems 越准确；TTL 越长，Mongo 压力越小。
	messageCountCacheTTL = 500 * time.Millisecond
	// messageCountSweepInterval 控制缓存清理频率，避免 sync.Map 长期增长。
	messageCountSweepInterval = 1 * time.Minute
)

func (s *Store) countMessagesCached(ctx context.Context, accountID bson.ObjectID) (int64, error) {
	return s.countMessagesCachedWithFilter(ctx, accountID, nil)
}

func (s *Store) countMessagesCachedWithFilter(ctx context.Context, accountID bson.ObjectID, seen *bool) (int64, error) {
	if s == nil || s.messages == nil {
		return 0, errors.New("store not initialized")
	}

	key := accountID.Hex()
	filter := bson.M{"accountId": accountID, "isDeleted": false}
	if seen != nil {
		key = key + ":seen:" + strconv.FormatBool(*seen)
		filter["seen"] = *seen
	}
	nowUnix := time.Now().UnixNano()

	if v, ok := s.messageCountCache.Load(key); ok {
		if e, ok := v.(*messageCountEntry); ok && e != nil {
			if e.expiresAt > nowUnix {
				s.maybeSweepMessageCountCache(nowUnix)
				return e.count, nil
			}
			s.messageCountCache.Delete(key)
		} else {
			s.messageCountCache.Delete(key)
		}
	}

	// 并发合并：同一时刻同一 account 只做一次 CountDocuments。
	// 注意：实际 Mongo 查询使用后台 ctx + 超时，避免某个等待方 ctx 取消导致整组都失败。
	ch := s.messageCountSF.DoChan(key, func() (any, error) {
		nowUnix := time.Now().UnixNano()
		if v, ok := s.messageCountCache.Load(key); ok {
			if e, ok := v.(*messageCountEntry); ok && e != nil && e.expiresAt > nowUnix {
				return e.count, nil
			}
		}

		qCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		total, err := s.messages.CountDocuments(qCtx, filter)
		if err != nil {
			return int64(0), err
		}

		exp := time.Now().Add(messageCountCacheTTL).UnixNano()
		s.messageCountCache.Store(key, &messageCountEntry{count: total, expiresAt: exp})
		s.maybeSweepMessageCountCache(nowUnix)
		return total, nil
	})

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return 0, res.Err
		}
		n, ok := res.Val.(int64)
		if !ok {
			return 0, fmt.Errorf("unexpected count result type %T", res.Val)
		}
		return n, nil
	}
}

func (s *Store) maybeSweepMessageCountCache(nowUnixNano int64) {
	if s == nil {
		return
	}

	last := s.messageCountLastSweep.Load()
	if last != 0 && time.Duration(nowUnixNano-last) < messageCountSweepInterval {
		return
	}
	if !s.messageCountLastSweep.CompareAndSwap(last, nowUnixNano) {
		return
	}

	s.messageCountCache.Range(func(k, v any) bool {
		e, ok := v.(*messageCountEntry)
		if !ok || e == nil || e.expiresAt <= nowUnixNano {
			s.messageCountCache.Delete(k)
		}
		return true
	})
}
