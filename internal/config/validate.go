package config

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Validate 用于 CLI 的 --check-config：做基础合法性校验（不校验外部依赖连通性）。
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	if err := validatePort(cfg.Server.API.Port, "server.api.port"); err != nil {
		return err
	}
	if err := validatePort(cfg.Server.SMTP.Port, "server.smtp.port"); err != nil {
		return err
	}

	if err := validateDialects(cfg); err != nil {
		return err
	}

	if cfg.Server.API.Debug.Enabled {
		if err := validateDebug(cfg.Server.API.Debug, "server.api.debug"); err != nil {
			return err
		}
	}
	if cfg.Server.SMTP.Debug.Enabled {
		if err := validateDebug(cfg.Server.SMTP.Debug, "server.smtp.debug"); err != nil {
			return err
		}
	}
	if cfg.Server.Worker.Debug.Enabled {
		if err := validateDebug(cfg.Server.Worker.Debug, "server.worker.debug"); err != nil {
			return err
		}
	}

	return nil
}

// ValidateAPIConfig 校验 API 服务所需配置（不做外部依赖连通性探测）。
func ValidateAPIConfig(cfg *Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}

	if strings.TrimSpace(cfg.MongoDB.URI) == "" {
		return fmt.Errorf("mongodb.uri is required")
	}
	if strings.TrimSpace(cfg.MongoDB.Database) == "" {
		return fmt.Errorf("mongodb.database is required")
	}
	if strings.TrimSpace(cfg.Redis.Addr) == "" {
		return fmt.Errorf("redis.addr is required")
	}
	if strings.TrimSpace(cfg.MinIO.Endpoint) == "" {
		return fmt.Errorf("minio.endpoint is required")
	}
	if strings.TrimSpace(cfg.MinIO.Bucket) == "" {
		return fmt.Errorf("minio.bucket is required")
	}
	if strings.TrimSpace(cfg.MinIO.AccessKey) == "" {
		return fmt.Errorf("minio.accessKey is required")
	}
	if strings.TrimSpace(cfg.MinIO.SecretKey) == "" {
		return fmt.Errorf("minio.secretKey is required")
	}

	if strings.TrimSpace(cfg.JWT.Secret) == "" {
		return fmt.Errorf("jwt.secret is required")
	}
	if cfg.JWT.Expiry <= 0 {
		return fmt.Errorf("jwt.expiry must be > 0")
	}

	// cfworker 上游可选，但若配置了必须是有效 URL。
	if s := strings.TrimSpace(cfg.Dialects.CFWorker.Upstream); s != "" {
		u, err := url.Parse(s)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("dialects.cfworker.upstream must be a valid URL, got %q", cfg.Dialects.CFWorker.Upstream)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("dialects.cfworker.upstream must use http/https scheme, got %q", u.Scheme)
		}
	}

	return nil
}

// ValidateSMTPConfig 校验 SMTP 服务所需配置（不做外部依赖连通性探测）。
func ValidateSMTPConfig(cfg *Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}

	if strings.TrimSpace(cfg.Redis.Addr) == "" {
		return fmt.Errorf("redis.addr is required")
	}
	if strings.TrimSpace(cfg.NATS.URL) == "" {
		return fmt.Errorf("nats.url is required")
	}
	if strings.TrimSpace(cfg.NATS.Stream) == "" {
		return fmt.Errorf("nats.stream is required")
	}
	if strings.TrimSpace(cfg.NATS.Subject) == "" {
		return fmt.Errorf("nats.subject is required")
	}

	if cfg.Server.SMTP.MaxMessageBytes <= 0 {
		return fmt.Errorf("server.smtp.maxMessageBytes must be > 0")
	}
	if cfg.Server.SMTP.MaxRecipients <= 0 {
		return fmt.Errorf("server.smtp.maxRecipients must be > 0")
	}

	// SMTP TLS/STARTTLS（可选）
	if cfg.Server.SMTP.TLS.RequireTLS && !cfg.Server.SMTP.TLS.Enabled {
		return fmt.Errorf("server.smtp.tls.requireTLS requires server.smtp.tls.enabled=true")
	}
	if cfg.Server.SMTP.TLS.Enabled {
		if strings.TrimSpace(cfg.Server.SMTP.TLS.CertFile) == "" {
			return fmt.Errorf("server.smtp.tls.certFile is required when server.smtp.tls.enabled=true")
		}
		if strings.TrimSpace(cfg.Server.SMTP.TLS.KeyFile) == "" {
			return fmt.Errorf("server.smtp.tls.keyFile is required when server.smtp.tls.enabled=true")
		}
		if s := strings.TrimSpace(cfg.Server.SMTP.TLS.MinVersion); s != "" {
			if _, ok := ParseTLSMinVersion(s); !ok {
				return fmt.Errorf("server.smtp.tls.minVersion must be 1.2 or 1.3, got %q", cfg.Server.SMTP.TLS.MinVersion)
			}
		}
		// 提前检查文件可读性（不做更深的证书内容校验）。
		if _, err := os.Stat(cfg.Server.SMTP.TLS.CertFile); err != nil {
			return fmt.Errorf("server.smtp.tls.certFile not accessible: %v", err)
		}
		if _, err := os.Stat(cfg.Server.SMTP.TLS.KeyFile); err != nil {
			return fmt.Errorf("server.smtp.tls.keyFile not accessible: %v", err)
		}
	}

	return nil
}

// ValidateWorkerConfig 校验 Worker 服务所需配置（不做外部依赖连通性探测）。
func ValidateWorkerConfig(cfg *Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}

	if strings.TrimSpace(cfg.MongoDB.URI) == "" {
		return fmt.Errorf("mongodb.uri is required")
	}
	if strings.TrimSpace(cfg.MongoDB.Database) == "" {
		return fmt.Errorf("mongodb.database is required")
	}
	if strings.TrimSpace(cfg.Redis.Addr) == "" {
		return fmt.Errorf("redis.addr is required")
	}
	if strings.TrimSpace(cfg.NATS.URL) == "" {
		return fmt.Errorf("nats.url is required")
	}
	if strings.TrimSpace(cfg.NATS.Stream) == "" {
		return fmt.Errorf("nats.stream is required")
	}
	if strings.TrimSpace(cfg.NATS.Subject) == "" {
		return fmt.Errorf("nats.subject is required")
	}
	if cfg.NATS.ConsumerAckWait <= 0 {
		return fmt.Errorf("nats.consumerAckWait must be > 0")
	}
	if cfg.NATS.ConsumerMaxDeliver <= 0 {
		return fmt.Errorf("nats.consumerMaxDeliver must be > 0")
	}
	if strings.TrimSpace(cfg.MinIO.Endpoint) == "" {
		return fmt.Errorf("minio.endpoint is required")
	}
	if strings.TrimSpace(cfg.MinIO.Bucket) == "" {
		return fmt.Errorf("minio.bucket is required")
	}
	if strings.TrimSpace(cfg.MinIO.AccessKey) == "" {
		return fmt.Errorf("minio.accessKey is required")
	}
	if strings.TrimSpace(cfg.MinIO.SecretKey) == "" {
		return fmt.Errorf("minio.secretKey is required")
	}

	return nil
}

func validatePort(port int, name string) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("%s must be in range 1-65535, got %d", name, port)
	}
	return nil
}

func validateDebug(d DebugServerConfig, name string) error {
	host := strings.TrimSpace(d.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("%s.host must be loopback (127.0.0.1/::1/localhost), got %q", name, d.Host)
	}
	if err := validatePort(d.Port, name+".port"); err != nil {
		return err
	}
	return nil
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

func validateDialects(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	// 仅当启用 baseHost 时才校验 dialect 路由相关配置。
	baseHost := strings.TrimSpace(cfg.Server.API.BaseHost)
	if baseHost == "" {
		return nil
	}

	// baseHost 容错：允许写成 https://api.xxx.com 或带路径/端口，但最终必须能归一化成 host。
	h := strings.ToLower(strings.TrimSpace(baseHost))
	if i := strings.Index(h, "://"); i != -1 {
		h = h[i+3:]
	}
	if j := strings.IndexByte(h, '/'); j != -1 {
		h = h[:j]
	}
	h = strings.TrimSuffix(h, ".")

	// 去端口（只处理常见 host:port；但也尽量容错 IPv6 bracket 写法：[::1]:8080）。
	if strings.HasPrefix(h, "[") {
		if end := strings.IndexByte(h, ']'); end != -1 {
			h = h[1:end]
		} else {
			h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
		}
	} else if i := strings.LastIndexByte(h, ':'); i != -1 && strings.IndexByte(h, ':') == i {
		// 只有一个 ':' 才可能是 host:port
		hostOnly, _, err := net.SplitHostPort(h)
		if err != nil {
			return fmt.Errorf("server.api.baseHost is invalid, got %q", cfg.Server.API.BaseHost)
		}
		h = hostOnly
	}
	if strings.TrimSpace(h) == "" {
		return fmt.Errorf("server.api.baseHost is invalid, got %q", cfg.Server.API.BaseHost)
	}

	if s := strings.TrimSpace(cfg.Server.API.UnknownDialect); s != "" {
		switch strings.ToLower(s) {
		case "reject", "fallback":
		default:
			return fmt.Errorf("server.api.unknownDialect must be reject or fallback, got %q", cfg.Server.API.UnknownDialect)
		}
	}

	// enabledDialects 中不允许包含 '.'（只允许单 label 名称）。
	for _, d := range cfg.Server.API.EnabledDialects {
		name := strings.TrimSpace(d)
		if name == "" {
			continue
		}
		if strings.Contains(name, ".") {
			return fmt.Errorf("server.api.enabledDialects contains invalid name %q (must not contain '.')", d)
		}
	}

	return nil
}

func ParseTLSMinVersion(s string) (uint16, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "1.2", "tls1.2", "tls12":
		return tls.VersionTLS12, true
	case "1.3", "tls1.3", "tls13":
		return tls.VersionTLS13, true
	default:
		return 0, false
	}
}
