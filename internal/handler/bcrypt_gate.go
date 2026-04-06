package handler

import (
	"context"
	"errors"
	"time"
)

var errBcryptBusy = errors.New("bcrypt concurrency limit reached")

func (h *Handler) withBcryptPermit(ctx context.Context, fn func() error) error {
	if h.bcryptSem == nil {
		return fn()
	}

	// 快速路径：不阻塞拿到令牌。
	select {
	case h.bcryptSem <- struct{}{}:
		defer func() { <-h.bcryptSem }()
		return fn()
	default:
	}

	// 令牌耗尽：最多等待一小段时间，避免高并发时 goroutine 无限堆积。
	const maxWait = 2 * time.Second
	timer := time.NewTimer(maxWait)
	defer timer.Stop()

	select {
	case h.bcryptSem <- struct{}{}:
		defer func() { <-h.bcryptSem }()
		return fn()
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errBcryptBusy
	}
}

