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
	// ListMessagesAfter is a seek-based pagination to avoid large skip costs.
	// cursorID is a message _id (hex). Empty cursorID means first page.
	ListMessagesAfter(ctx context.Context, accountID bson.ObjectID, cursorID string, limit int) ([]model.Message, int64, error)
	// UpdateMessageFlags updates message flags in a single MongoDB round-trip.
	// nil means "unchanged".
	UpdateMessageFlags(ctx context.Context, id string, seen, keep *bool) error
	UpdateMessageSeen(ctx context.Context, id string, seen bool) error
	UpdateMessageKeep(ctx context.Context, id string, keep bool) error
	DeleteMessage(ctx context.Context, id string) error
	HardDeleteMessagesByAccount(ctx context.Context, accountID bson.ObjectID) ([]model.Message, error)
	CountMessagesByAccount(ctx context.Context, accountID bson.ObjectID) (int64, error)
	Close(ctx context.Context) error
}
