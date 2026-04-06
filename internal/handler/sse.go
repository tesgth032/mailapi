package handler

import (
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (h *Handler) SSE(c *gin.Context) {
	accountID := c.GetString("accountId")

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering

	// Subscribe to this account's event stream (shared Redis subscription + local fan-out).
	var ch <-chan string
	var unsubscribe func()
	if h.sseHub != nil {
		ch, unsubscribe = h.sseHub.Subscribe(accountID)
	} else {
		// 兜底：理论上不会发生。
		sub := h.cache.Subscribe(c.Request.Context(), accountID)
		defer sub.Close()
		rawCh := sub.Channel()
		tmp := make(chan string, 16)
		go func() {
			defer close(tmp)
			for m := range rawCh {
				tmp <- m.Payload
			}
		}()
		ch = tmp
		unsubscribe = func() {}
	}
	defer unsubscribe()

	// 心跳：保持长连接活跃，并帮助代理/负载均衡及时发现断链。
	const pingInterval = 15 * time.Second
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	c.Stream(func(w io.Writer) bool {
		select {
		case payload, ok := <-ch:
			if !ok {
				return false
			}
			c.SSEvent("message", payload)
			return true
		case <-ping.C:
			// SSE 注释行心跳，不触发客户端事件回调。
			_, _ = w.Write([]byte(": ping\n\n"))
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})

	c.Status(http.StatusOK)
}
