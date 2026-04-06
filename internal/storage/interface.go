package storage

import (
	"context"
	"io"
	"time"
)

// Interface defines all object storage operations. Implemented by *Storage.
type Interface interface {
	// Health 用于 readiness 检查：验证对象存储可用性（轻量）。
	Health(ctx context.Context) error
	Upload(ctx context.Context, messageID, attachmentID, filename, contentType string, data []byte) error
	UploadReader(ctx context.Context, messageID, attachmentID, filename, contentType string, r io.Reader, size int64) error
	Open(ctx context.Context, messageID, attachmentID, filename string) (io.ReadCloser, string, int64, error)
	Download(ctx context.Context, messageID, attachmentID, filename string) ([]byte, string, error)
	GetPresignedURL(ctx context.Context, messageID, attachmentID, filename string, expiry time.Duration) (string, error)
	DeleteByMessage(ctx context.Context, messageID string) error
	// ListMessageIDs 列举对象存储中出现过的 messageID（从 object key 的 `messages/<id>/...` 中提取）。
	// 返回值 nextStartAfter 用于增量扫描（传回下一次调用），避免每次全量扫 bucket。
	ListMessageIDs(ctx context.Context, startAfter string, maxObjects int) (ids []string, nextStartAfter string, err error)
	UploadRawMessage(ctx context.Context, messageID string, data []byte) error
	OpenRawMessage(ctx context.Context, messageID string) (io.ReadCloser, string, int64, error)
}
