package cfworker

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config 定义 cfworker（cloudflare_temp_email 风格）代理的基础配置。
type Config struct {
	// Upstream 是上游 Worker 的 Base URL，例如：
	// - https://temp-mail.example.workers.dev
	// - http://127.0.0.1:8787
	Upstream string
	// Timeout 是每个请求的最大耗时（包含请求与响应体传输）。
	// 0 表示不额外设置（仅受请求 ctx 影响）。
	Timeout time.Duration
}

// NewProxy 返回一个 http.Handler，用于将请求按原路径/查询/方法代理到 cfworker 上游。
//
// 设计目标：
// - 尽量流式转发（不把 request/response body 全量读入内存）
// - 透传鉴权头（Authorization / x-admin-auth / x-user-token / x-custom-auth）
// - 移除 hop-by-hop headers，避免代理链问题
// - 使用可复用连接的 Transport，适合高并发场景
func NewProxy(cfg Config) http.Handler {
	upstream, err := parseUpstream(cfg.Upstream)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "cfworker upstream not configured\n")
		})
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		// 作为反向代理：不做自动解压缩，避免在高并发场景下把 gzip 解压成本转嫁到本服务 CPU。
		DisableCompression: true,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          1024,
		MaxIdleConnsPerHost:   256,
		MaxConnsPerHost:       0, // 不限制；如需背压建议由外层实现
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}

	client := &http.Client{Transport: transport}

	// 复用拷贝缓冲区，降低高并发下的分配与 GC 压力。
	var bufPool sync.Pool
	bufPool.New = func() any { return make([]byte, 32*1024) }

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var cancel context.CancelFunc
		if cfg.Timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
			defer cancel()
		}

		target := buildTargetURL(upstream, r.URL)

		origHost := r.Host
		outReq := r.Clone(ctx)
		outReq.URL = target
		outReq.Host = upstream.Host
		outReq.RequestURI = "" // client request must not set RequestURI

		// 透传所有请求头，但移除 hop-by-hop headers（并不会删除 Authorization 等敏感鉴权头）。
		removeHopByHopHeaders(outReq.Header)

		// 增加标准的 forwarded 头，便于上游做审计/排障。
		setForwardedHeaders(outReq, origHost)

		resp, err := client.Do(outReq)
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, "cfworker upstream error\n")
			return
		}
		defer resp.Body.Close()

		// 响应头同样移除 hop-by-hop headers，避免代理链误用。
		removeHopByHopHeaders(resp.Header)
		copyHeader(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)

		buf := bufPool.Get().([]byte)
		_, _ = io.CopyBuffer(w, resp.Body, buf)
		bufPool.Put(buf)
	})
}

func parseUpstream(s string) (*url.URL, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty upstream")
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("invalid upstream scheme")
	}
	if u.Host == "" {
		return nil, errors.New("missing upstream host")
	}
	return u, nil
}

func buildTargetURL(upstream *url.URL, in *url.URL) *url.URL {
	out := *upstream

	// Path
	out.Path = singleJoiningSlash(upstream.Path, in.Path)

	// Query（合并上游 query 与原请求 query）
	switch {
	case upstream.RawQuery == "":
		out.RawQuery = in.RawQuery
	case in.RawQuery == "":
		out.RawQuery = upstream.RawQuery
	default:
		out.RawQuery = upstream.RawQuery + "&" + in.RawQuery
	}

	return &out
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		if a == "" || b == "" {
			return a + b
		}
		return a + "/" + b
	default:
		return a + b
	}
}

var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection", // 非标准，但一些客户端会带
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopByHopHeaders(h http.Header) {
	// RFC 7230: Connection header may include additional hop-by-hop headers.
	if c := h.Get("Connection"); c != "" {
		for _, f := range strings.Split(c, ",") {
			if f = strings.TrimSpace(f); f != "" {
				h.Del(f)
			}
		}
	}
	for _, k := range hopByHopHeaders {
		h.Del(k)
	}
}

func setForwardedHeaders(outReq *http.Request, origHost string) {
	// X-Forwarded-Host: 原始 Host（便于上游识别入口域名）
	if origHost != "" {
		outReq.Header.Set("X-Forwarded-Host", origHost)
	}

	// X-Forwarded-Proto
	if outReq.Header.Get("X-Forwarded-Proto") == "" {
		if outReq.TLS != nil {
			outReq.Header.Set("X-Forwarded-Proto", "https")
		} else {
			outReq.Header.Set("X-Forwarded-Proto", "http")
		}
	}

	// X-Forwarded-For: 追加客户端 IP
	if ip := remoteIP(outReq.RemoteAddr); ip != "" {
		if prior := outReq.Header.Get("X-Forwarded-For"); prior != "" {
			outReq.Header.Set("X-Forwarded-For", prior+", "+ip)
		} else {
			outReq.Header.Set("X-Forwarded-For", ip)
		}
	}
}

func remoteIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	// RemoteAddr 通常是 "ip:port"
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	// 若无法 split，尝试整体当作 IP（兼容无端口场景）
	return remoteAddr
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
