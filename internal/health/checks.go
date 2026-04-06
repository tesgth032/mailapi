package health

import (
	"context"
	"net/http"
	"strings"
)

// Check 表示一个 readiness 检查项。
// Name 会作为 JSON checks map 的 key。
type Check struct {
	Name  string
	Check func(ctx context.Context) error
}

// NewReadyzChecks 返回一个 readiness handler：并发执行 checks，并输出 JSON。
//
// 输出格式与 NewReadyz 保持一致：
//   - status: "ready" / "not ready"
//   - checks: map[name] => "ok" / err string
//
// 注意：该 handler 不做鉴权，建议仅在本地 debug server 上暴露。
func NewReadyzChecks(checks []Check, opts Options) http.Handler {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}

	type normalized struct {
		name string
		fn   func(ctx context.Context) error
	}

	norm := make([]normalized, 0, len(checks))
	for i := range checks {
		name := strings.TrimSpace(checks[i].Name)
		if name == "" || checks[i].Check == nil {
			continue
		}
		norm = append(norm, normalized{name: name, fn: checks[i].Check})
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

		ch := make(chan result, len(norm))
		for i := range norm {
			n := norm[i]
			go func() { ch <- result{name: n.name, err: n.fn(ctx)} }()
		}

		checksOut := make(map[string]string, len(norm))
		ready := true

		for i := 0; i < len(norm); i++ {
			select {
			case res := <-ch:
				if res.err != nil {
					ready = false
					checksOut[res.name] = res.err.Error()
				} else {
					checksOut[res.name] = "ok"
				}
			case <-ctx.Done():
				ready = false
				checksOut["timeout"] = ctx.Err().Error()
				i = len(norm)
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
			"checks": checksOut,
		})
	})
}
