package dialect

import (
	"net/http"
	"strings"
)

type unknownDialectStrategy uint8

const (
	unknownReject unknownDialectStrategy = iota
	unknownFallback
)

// Dispatcher 根据请求 Host 选择不同的 API 风格（dialect）的 handler。
//
// 规则（简化版）：
// - host == baseHost             -> defaultDialect
// - host == <dialect>.<baseHost> -> <dialect>（仅允许单 label 前缀）
// - host 不匹配 baseHost 或 *.baseHost -> defaultDialect（用于 localhost/IP 等场景）
//
// enabledDialects 为空表示不限制（允许所有已注册 dialect）。
// unknownDialect 支持 "reject" 或 "fallback"。
type Dispatcher struct {
	baseHost       string
	baseSuffix     string
	defaultDialect string
	enabled        map[string]struct{}
	unknown        unknownDialectStrategy

	handlers map[string]http.Handler
}

func NewDispatcher(baseHost, defaultDialect string, enabledDialects []string, unknownDialect string) *Dispatcher {
	d := &Dispatcher{
		baseHost:       normalizeBaseHost(baseHost),
		defaultDialect: strings.ToLower(strings.TrimSpace(defaultDialect)),
		unknown:        parseUnknownDialectStrategy(unknownDialect),
		handlers:       make(map[string]http.Handler),
	}
	if d.baseHost != "" {
		d.baseSuffix = "." + d.baseHost
	}
	if d.defaultDialect == "" {
		d.defaultDialect = "duck"
	}
	if len(enabledDialects) > 0 {
		d.enabled = make(map[string]struct{}, len(enabledDialects))
		for _, s := range enabledDialects {
			name := strings.ToLower(strings.TrimSpace(s))
			if name == "" {
				continue
			}
			d.enabled[name] = struct{}{}
		}
	}
	return d
}

// Register registers a dialect handler. name 会被归一为小写。
func (d *Dispatcher) Register(name string, h http.Handler) {
	if d == nil || h == nil {
		return
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || strings.Contains(name, ".") {
		return
	}
	d.handlers[name] = h
}

func (d *Dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if d == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// baseHost 为空表示禁用：所有请求走默认 dialect（保持向后兼容）。
	if d.baseHost == "" {
		d.serveDefault(w, r)
		return
	}

	dialectName, matchedBase := d.pickDialect(r.Host)
	if !matchedBase {
		// host 不匹配 baseHost：用于 localhost/IP 等；始终走默认 dialect。
		d.serveDefault(w, r)
		return
	}

	if d.isEnabled(dialectName) {
		if h, ok := d.handlers[dialectName]; ok {
			h.ServeHTTP(w, r)
			return
		}
	}

	// host 匹配 baseHost，但 dialect 未注册或不在 enabled 列表内：按策略处理。
	if d.unknown == unknownFallback {
		d.serveDefault(w, r)
		return
	}

	http.NotFound(w, r)
}

func (d *Dispatcher) serveDefault(w http.ResponseWriter, r *http.Request) {
	if d == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	if d.isEnabled(d.defaultDialect) {
		if h, ok := d.handlers[d.defaultDialect]; ok {
			h.ServeHTTP(w, r)
			return
		}
	}

	// 配置错误：默认 dialect 不可用。对外返回 503。
	http.Error(w, "service unavailable", http.StatusServiceUnavailable)
}

func (d *Dispatcher) isEnabled(name string) bool {
	if d == nil {
		return false
	}
	if len(d.enabled) == 0 {
		return true
	}
	_, ok := d.enabled[name]
	return ok
}

func (d *Dispatcher) pickDialect(host string) (dialect string, matchedBase bool) {
	h := normalizeHost(host)
	if h == "" {
		return d.defaultDialect, false
	}
	if h == d.baseHost {
		return d.defaultDialect, true
	}

	suffix := d.baseSuffix
	if suffix == "" || !strings.HasSuffix(h, suffix) {
		return d.defaultDialect, false
	}

	prefix := h[:len(h)-len(suffix)]
	if prefix == "" {
		return d.defaultDialect, true
	}
	// 仅允许单 label：不允许包含点。
	if strings.IndexByte(prefix, '.') != -1 {
		return "", true
	}
	return prefix, true
}

func parseUnknownDialectStrategy(s string) unknownDialectStrategy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "fallback":
		return unknownFallback
	default:
		return unknownReject
	}
}

func normalizeBaseHost(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}

	// 容错：允许用户误写成 https://api.xxx.com 或带路径。
	if i := strings.Index(s, "://"); i != -1 {
		s = s[i+3:]
	}
	if j := strings.IndexByte(s, '/'); j != -1 {
		s = s[:j]
	}

	s = normalizeHost(s)
	s = strings.TrimSuffix(s, ".")
	return s
}

// normalizeHost 归一化 host：去端口、转小写、去尾部点。
func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}

	// 去掉可能的尾部点（FQDN 格式）。
	host = strings.TrimSuffix(host, ".")

	// 兼容 IPv6 bracket 写法（如 "[::1]:8080"）。
	// 注意：Host header 的标准写法是 bracket + :port，因此这里优先处理 bracket 形式。
	if strings.HasPrefix(host, "[") {
		if end := strings.IndexByte(host, ']'); end != -1 {
			// [ipv6] 或 [ipv6]:port
			return host[1:end]
		}
		// 非法但尽量容错：去掉两侧括号后返回。
		return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}

	// 常见形式：example.com:8080
	if i := strings.LastIndexByte(host, ':'); i != -1 {
		// 若仅存在一个 ':'，则视为 host:port 形式并剥离端口。
		if strings.IndexByte(host, ':') == i {
			host = host[:i]
		}
	}

	return host
}
