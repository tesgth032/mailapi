package handler

import (
	"context"
	"time"
)

// tryAsyncStorageCleanup 尝试异步清理某个 message 前缀下的对象（raw + attachments）。
// 若当前清理并发已满，会直接跳过（由后台对象 GC 兜底），避免在高并发删除场景下放大负载。
func (h *Handler) tryAsyncStorageCleanup(messageID string) {
	if h == nil || h.storage == nil || h.storageCleanupSem == nil {
		return
	}

	select {
	case h.storageCleanupSem <- struct{}{}:
		go func() {
			defer func() { <-h.storageCleanupSem }()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_ = h.storage.DeleteByMessage(ctx, messageID)
			cancel()
		}()
	default:
		// 并发已满：跳过本次清理，依赖后台 GC 兜底。
	}
}

