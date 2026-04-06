package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// StorePinger 表示一个可被 readiness 探测的存储后端（例如 MongoDB）。
type StorePinger interface {
	Ping(ctx context.Context) error
}

// CachePinger 表示一个可被 readiness 探测的缓存后端（例如 Redis）。
type CachePinger interface {
	Ping(ctx context.Context) error
}

// StorageHealthChecker 表示一个可被 readiness 探测的对象存储后端（例如 MinIO/S3）。
type StorageHealthChecker interface {
	Health(ctx context.Context) error
}

type ReadinessDeps struct {
	Store   StorePinger
	Cache   CachePinger
	Storage StorageHealthChecker
}

type Options struct {
	// Timeout 为单次 readiness 探测的总体超时（<=0 使用默认值）。
	Timeout time.Duration
}

const defaultProbeTimeout = 2 * time.Second

// NewHealthz 返回 liveness handler：仅表示进程存活，不探测外部依赖。
func NewHealthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
}

// NewReadyz 返回 readiness handler：探测依赖连通性（store/cache/storage）。
func NewReadyz(deps ReadinessDeps, opts Options) http.Handler {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		var cancel context.CancelFunc
		if timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		type result struct {
			name string
			err  error
		}

		ch := make(chan result, 3)
		n := 0
		if deps.Store != nil {
			n++
			go func() { ch <- result{name: "store", err: deps.Store.Ping(ctx)} }()
		}
		if deps.Cache != nil {
			n++
			go func() { ch <- result{name: "cache", err: deps.Cache.Ping(ctx)} }()
		}
		if deps.Storage != nil {
			n++
			go func() { ch <- result{name: "storage", err: deps.Storage.Health(ctx)} }()
		}

		checks := make(map[string]string, n)
		ready := true

		for i := 0; i < n; i++ {
			select {
			case res := <-ch:
				if res.err != nil {
					ready = false
					checks[res.name] = res.err.Error()
				} else {
					checks[res.name] = "ok"
				}
			case <-ctx.Done():
				ready = false
				checks["timeout"] = ctx.Err().Error()
				// 返回即可：channel 有缓冲，后续 goroutine 发送不会阻塞。
				i = n
			}
		}

		status := http.StatusOK
		outStatus := "ready"
		if !ready {
			status = http.StatusServiceUnavailable
			outStatus = "not ready"
		}
		writeJSON(w, status, map[string]any{
			"status": outStatus,
			"checks": checks,
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}
