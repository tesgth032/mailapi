package store

import (
	"context"

	"mailapi/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Interface defines all store operations. Implemented by *Store.
type Interface interface {
	// Ping 用于 readiness 检查：验证 MongoDB 可用性（轻量，不做业务读写）。
	Ping(ctx context.Context) error
	ListDomains(ctx context.Context) ([]model.Domain, error)
	GetDomainByName(ctx context.Context, domain string) (*model.Domain, error)
	CreateDomain(ctx context.Context, domain *model.Domain) error
	SyncDomains(ctx context.Context, domains []model.Domain) error
	CreateAccount(ctx context.Context, account *model.Account) error
	GetAccount(ctx context.Context, id string) (*model.Account, error)
	// GetAccountIDByAddress is a lightweight lookup used by workers under high concurrency.
	// 仅返回 _id，避免解码 password/quota 等无关字段。
	GetAccountIDByAddress(ctx context.Context, address string) (bson.ObjectID, error)
	GetAccountByAddress(ctx context.Context, address string) (*model.Account, error)
	DeleteAccount(ctx context.Context, id string) error
	UpdateAccountUsed(ctx context.Context, id bson.ObjectID, delta int64) error
	// TryReserveAccountUsed 原子地“预占用”账号配额：仅当 used+delta 不超过 quota 时才会 +delta。
	// ok=false 表示配额不足或账号不存在（调用方可按需再判定/纠偏）。
	TryReserveAccountUsed(ctx context.Context, id bson.ObjectID, delta int64) (ok bool, err error)
	// RecalculateAccountUsed 通过聚合 messages（isDeleted=false）重新计算账号 used，并写回 accounts.used。
	// 返回 recomputed used。用于 TTL 自动删除/异常回滚导致 used 漂移时的纠偏。
	RecalculateAccountUsed(ctx context.Context, id bson.ObjectID) (int64, error)
	CreateMessage(ctx context.Context, msg *model.Message) error
	GetMessage(ctx context.Context, id string) (*model.Message, error)
	// GetMessageMeta returns message metadata without heavy fields (rawMessage/text/html).
	// 用于删除/附件下载等只需要元数据的场景，避免高并发下 Mongo 解码大字段导致 CPU/IO 飙升。
	GetMessageMeta(ctx context.Context, id string) (*model.Message, error)
	// GetMessageRaw returns only rawMessage (and accountId) for .eml 下载。
	GetMessageRaw(ctx context.Context, id string) (*model.Message, error)
	// HasMessage 用于高并发/幂等场景的轻量存在性检查。
	HasMessage(ctx context.Context, id string) (bool, error)
	// HasMessageByIngest 用于 JetStream 幂等去重：同一个（stream,seq）对同一账号只处理一次。
	HasMessageByIngest(ctx context.Context, accountID bson.ObjectID, ingestStream string, ingestSeq int64) (bool, error)
	// ExistingMessageIDs 批量检查 message 是否存在（且未软删除），用于对象存储 GC 等批处理场景，减少往返次数。
	ExistingMessageIDs(ctx context.Context, ids []string) (map[string]struct{}, error)
	ListMessages(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error)
	// ListMessagesFiltered 支持常见筛选（例如 seen=true/false）。seen=nil 表示不筛选。
	ListMessagesFiltered(ctx context.Context, accountID bson.ObjectID, page, perPage int, seen *bool) ([]model.Message, int64, error)
	// ListMessagesAfter is a seek-based pagination to avoid large skip costs.
	// cursorID is a message _id (hex). Empty cursorID means first page.
	ListMessagesAfter(ctx context.Context, accountID bson.ObjectID, cursorID string, limit int) ([]model.Message, int64, error)
	// ListMessagesAfterFiltered 是 seek 分页的筛选版本。seen=nil 表示不筛选。
	ListMessagesAfterFiltered(ctx context.Context, accountID bson.ObjectID, cursorID string, limit int, seen *bool) ([]model.Message, int64, error)
	// UpdateMessageFlags updates message flags in a single MongoDB round-trip.
	// nil means "unchanged".
	UpdateMessageFlags(ctx context.Context, id string, seen, keep *bool) error
	// BulkUpdateMessageFlagsByIDs 批量更新指定消息的 flags（仅对当前 account 生效）。
	// 返回 modifiedCount。
	BulkUpdateMessageFlagsByIDs(ctx context.Context, accountID bson.ObjectID, ids []string, seen, keep *bool) (int64, error)
	// BulkUpdateMessageFlagsByAccount 批量更新该 account 下所有消息（isDeleted=false）。
	// 返回 modifiedCount。
	BulkUpdateMessageFlagsByAccount(ctx context.Context, accountID bson.ObjectID, seen, keep *bool) (int64, error)
	UpdateMessageSeen(ctx context.Context, id string, seen bool) error
	UpdateMessageKeep(ctx context.Context, id string, keep bool) error
	DeleteMessage(ctx context.Context, id string) error
	// SoftDeleteMessagesByAccount 批量软删除消息（isDeleted=true）。seen=nil 表示不筛选。
	// limit<=0 表示使用默认上限；返回实际删除的 messageIDs(hex) 与总 size（用于 quota 回收）。
	SoftDeleteMessagesByAccount(ctx context.Context, accountID bson.ObjectID, seen *bool, limit int) ([]string, int64, error)
	// SoftDeleteMessagesByIDs 按指定 ids 批量软删除（仅对当前 account 生效）。
	// 返回实际删除的 messageIDs(hex) 与总 size。
	SoftDeleteMessagesByIDs(ctx context.Context, accountID bson.ObjectID, ids []string) ([]string, int64, error)
	HardDeleteMessagesByAccount(ctx context.Context, accountID bson.ObjectID) ([]model.Message, error)
	CountMessagesByAccount(ctx context.Context, accountID bson.ObjectID) (int64, error)
	Close(ctx context.Context) error
}
