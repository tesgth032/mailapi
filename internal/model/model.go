package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Address represents an email address with optional display name.
type Address struct {
	Name    string `json:"name" bson:"name"`
	Address string `json:"address" bson:"address"`
}

// Attachment represents an email attachment's metadata.
type Attachment struct {
	ID               string `json:"id" bson:"id"`
	Filename         string `json:"filename" bson:"filename"`
	ContentType      string `json:"contentType" bson:"contentType"`
	Disposition      string `json:"disposition" bson:"disposition"`
	TransferEncoding string `json:"transferEncoding" bson:"transferEncoding"`
	Size             int64  `json:"size" bson:"size"`
}

// Domain represents an email domain available for temporary addresses.
type Domain struct {
	ID        bson.ObjectID `json:"id" bson:"_id,omitempty"`
	Domain    string        `json:"domain" bson:"domain"`
	IsActive  bool          `json:"isActive" bson:"isActive"`
	IsPrivate bool          `json:"isPrivate" bson:"isPrivate"`
	CreatedAt time.Time     `json:"createdAt" bson:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt" bson:"updatedAt"`
}

// Account represents a temporary email account.
type Account struct {
	ID        bson.ObjectID `json:"id" bson:"_id,omitempty"`
	Address   string        `json:"address" bson:"address"`
	Password  string        `json:"-" bson:"password"`
	Quota     int64         `json:"quota" bson:"quota"`
	Used      int64         `json:"used" bson:"used"`
	IsDeleted bool          `json:"isDeleted" bson:"isDeleted"`
	CreatedAt time.Time     `json:"createdAt" bson:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt" bson:"updatedAt"`
}

// Message represents an email message.
type Message struct {
	ID             bson.ObjectID `json:"id" bson:"_id,omitempty"`
	AccountID      bson.ObjectID `json:"accountId" bson:"accountId"`
	MsgID          string        `json:"msgid" bson:"msgid"`
	From           Address       `json:"from" bson:"from"`
	To             []Address     `json:"to" bson:"to"`
	Cc             []Address     `json:"cc,omitempty" bson:"cc,omitempty"`
	Bcc            []Address     `json:"bcc,omitempty" bson:"bcc,omitempty"`
	Subject        string        `json:"subject" bson:"subject"`
	Intro          string        `json:"intro" bson:"intro"`
	Text           string        `json:"text,omitempty" bson:"text"`
	HTML           []string      `json:"html,omitempty" bson:"html,omitempty"`
	HasAttachments bool          `json:"hasAttachments" bson:"hasAttachments"`
	Attachments    []Attachment  `json:"attachments,omitempty" bson:"attachments,omitempty"`
	Size           int64         `json:"size" bson:"size"`
	Seen           bool          `json:"seen" bson:"seen"`
	IsDeleted      bool          `json:"isDeleted" bson:"isDeleted"`
	// Keep 表示“长期保留（不受 message TTL 自动过期影响）”。默认 false。
	// 注意：该能力是否允许由 API 进程通过环境变量控制。
	Keep bool `json:"keep,omitempty" bson:"keep,omitempty"`
	// IngestStream/IngestSeq 用于幂等去重与排错（来自 JetStream 元数据）。不对外暴露。
	IngestStream string `json:"-" bson:"ingestStream,omitempty"`
	IngestSeq    int64  `json:"-" bson:"ingestSeq,omitempty"`
	Flagged        bool          `json:"flagged" bson:"flagged"`
	Retention      bool          `json:"retention" bson:"retention"`
	RetentionDate  time.Time     `json:"retentionDate" bson:"retentionDate"`
	DownloadURL    string        `json:"downloadUrl" bson:"-"`
	RawMessage     []byte        `json:"-" bson:"rawMessage,omitempty"`
	CreatedAt      time.Time     `json:"createdAt" bson:"createdAt"`
	UpdatedAt      time.Time     `json:"updatedAt" bson:"updatedAt"`
}

// IncomingEmail is the payload sent via NATS from SMTP to workers.
type IncomingEmail struct {
	From       string   `json:"from"`
	To         []string `json:"to"`
	RawMessage []byte   `json:"rawMessage"`
	RemoteAddr string   `json:"remoteAddr"`
	ReceivedAt int64    `json:"receivedAt"`

	// IngestStream/IngestSeq 来自 JetStream 元数据，仅用于消费端幂等与调试，不参与序列化。
	IngestStream string `json:"-"`
	IngestSeq    int64  `json:"-"`
}

// TokenRequest is the login request body.
type TokenRequest struct {
	Address  string `json:"address" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// TokenResponse is the login response.
type TokenResponse struct {
	Token string `json:"token"`
	ID    string `json:"id"`
}

// CreateAccountRequest is the account creation request body.
// Either Address (full email) or Domain (for auto-generated prefix) must be provided.
type CreateAccountRequest struct {
	Address  string `json:"address"`
	Domain   string `json:"domain"`
	Password string `json:"password" binding:"required,min=6"`
}

// UpdateMessageRequest is the message update request body.
type UpdateMessageRequest struct {
	Seen *bool `json:"seen"`
	// Keep=true 表示“长期保留”（不自动过期）；Keep=false 表示恢复 TTL 自动过期。
	Keep *bool `json:"keep"`
}

// HydraCollection wraps a list response in Hydra-compatible format.
type HydraCollection struct {
	Context    string `json:"@context"`
	ID         string `json:"@id"`
	Type       string `json:"@type"`
	TotalItems int64  `json:"hydra:totalItems"`
	Member     any    `json:"hydra:member"`
	// NextCursor 用于 seek 分页：客户端可将其作为 /messages?cursor=... 的下一页游标。
	// 为空表示没有更多数据或服务端未提供。
	NextCursor string `json:"nextCursor,omitempty"`
}
