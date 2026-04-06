package debugserver

import (
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"time"

	"mailapi/internal/config"
	"mailapi/internal/health"
	"mailapi/internal/metrics"
)

// New 返回一个已配置好的 debug HTTP server（/healthz /readyz /metrics /debug/pprof）。
// 注意：该 server 不做鉴权，且建议仅绑定到 loopback 地址。
func New(cfg config.DebugServerConfig, deps health.ReadinessDeps) (*http.Server, error) {
	return NewWithReadyHandler(cfg, health.NewReadyz(deps, health.Options{}))
}

// NewWithReadyHandler 返回一个已配置好的 debug HTTP server（/healthz /readyz /metrics /debug/pprof）。
// readyHandler 允许不同服务自定义 readiness 检查逻辑；传 nil 时使用默认 readyz（永远 ready）。
//
// 注意：该 server 不做鉴权，且建议仅绑定到 loopback 地址。
func NewWithReadyHandler(cfg config.DebugServerConfig, readyHandler http.Handler) (*http.Server, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("debug host must be loopback (127.0.0.1/::1/localhost), got %q", cfg.Host)
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("debug port must be in range 1-65535, got %d", cfg.Port)
	}

	addr := net.JoinHostPort(host, strconv.Itoa(cfg.Port))
	mux := http.NewServeMux()
	mux.Handle("/healthz", health.NewHealthz())
	if readyHandler != nil {
		mux.Handle("/readyz", readyHandler)
	} else {
		mux.Handle("/readyz", health.NewReadyz(health.ReadinessDeps{}, health.Options{}))
	}

	if cfg.Metrics {
		mux.Handle("/metrics", metrics.Handler())
	}
	if cfg.Pprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}

	// 兼容 IPv6 bracket 写法（虽然配置里一般不会这么写）。
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")

	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
