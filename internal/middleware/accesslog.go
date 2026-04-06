package middleware

import (
	"log"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// AccessLog 以低开销方式记录访问日志：支持采样、慢请求、仅错误。
//
// 设计目标：在高并发下避免像 gin.Logger() 那样“每个请求都格式化+写日志”，从而显著降低 CPU/I/O。
// - 普通请求：按 sampleEvery 采样（1=全量，100=百分之一）
// - 慢请求：latency >= slowThreshold 时总是记录（slowThreshold<=0 表示禁用）
// - 错误请求：status >= 400 时总是记录；errorsOnly=true 时仅记录错误请求
func AccessLog(sampleEvery int64, slowThreshold time.Duration, errorsOnly bool) gin.HandlerFunc {
	if sampleEvery <= 0 {
		sampleEvery = 1
	}

	var counter atomic.Uint64

	return func(c *gin.Context) {
		start := time.Now()
		method := c.Request.Method
		path := c.Request.URL.Path
		rawQuery := c.Request.URL.RawQuery

		c.Next()

		status := c.Writer.Status()
		latency := time.Since(start)
		isError := status >= 400
		isSlow := slowThreshold > 0 && latency >= slowThreshold

		if errorsOnly && !isError {
			return
		}

		if !isError && !isSlow && sampleEvery > 1 {
			n := counter.Add(1)
			if n%uint64(sampleEvery) != 0 {
				return
			}
		}

		fullPath := path
		if rawQuery != "" {
			// 仅在需要记录日志时再拼接，避免每次请求都分配新字符串。
			fullPath = path + "?" + rawQuery
		}

		log.Printf("access method=%s path=%s status=%d latency=%s remote=%s", method, fullPath, status, latency, c.Request.RemoteAddr)
	}
}

