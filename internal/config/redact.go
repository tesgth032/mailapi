package config

import "strings"

// Redact 返回一个“脱敏后的配置副本”，用于 --print-effective-config 默认输出。
// 注意：不会修改入参 cfg。
func Redact(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}

	out := *cfg

	// slices/maps 需要深拷贝，避免修改 out 时影响原始 cfg。
	if cfg.Domains != nil {
		out.Domains = append([]DomainConfig(nil), cfg.Domains...)
	}
	if cfg.APIKeys != nil {
		out.APIKeys = make([]APIKeyConfig, len(cfg.APIKeys))
		for i := range cfg.APIKeys {
			out.APIKeys[i] = cfg.APIKeys[i]
			out.APIKeys[i].Key = redactAPIKey(cfg.APIKeys[i].Key)
			if cfg.APIKeys[i].Domains != nil {
				out.APIKeys[i].Domains = append([]string(nil), cfg.APIKeys[i].Domains...)
			}
			if cfg.APIKeys[i].DomainLimits != nil {
				m := make(map[string]int64, len(cfg.APIKeys[i].DomainLimits))
				for k, v := range cfg.APIKeys[i].DomainLimits {
					m[k] = v
				}
				out.APIKeys[i].DomainLimits = m
			}
		}
	}

	if cfg.Server.API.TrustedProxies != nil {
		out.Server.API.TrustedProxies = append([]string(nil), cfg.Server.API.TrustedProxies...)
	}
	if cfg.Server.API.EnabledDialects != nil {
		out.Server.API.EnabledDialects = append([]string(nil), cfg.Server.API.EnabledDialects...)
	}

	// 直接覆盖敏感字段
	out.MongoDB = cfg.MongoDB
	out.MongoDB.URI = redactUserinfo(cfg.MongoDB.URI)

	out.Redis = cfg.Redis
	out.Redis.Password = redactSecret(cfg.Redis.Password)

	out.NATS = cfg.NATS
	out.NATS.URL = redactUserinfo(cfg.NATS.URL)

	out.MinIO = cfg.MinIO
	out.MinIO.AccessKey = redactSecret(cfg.MinIO.AccessKey)
	out.MinIO.SecretKey = redactSecret(cfg.MinIO.SecretKey)

	out.JWT = cfg.JWT
	out.JWT.Secret = redactSecret(cfg.JWT.Secret)

	return &out
}

func redactAPIKey(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(v, "sk_"):
		return "sk_<redacted>"
	case strings.HasPrefix(v, "dk_"):
		return "dk_<redacted>"
	default:
		return "<redacted>"
	}
}

func redactSecret(v string) string {
	if strings.TrimSpace(v) == "" {
		return v
	}
	return "<redacted>"
}

// redactUserinfo 会将形如 scheme://user:pass@host/... 的 userinfo 部分替换为 <redacted>。
// 若没有 userinfo，则原样返回。
func redactUserinfo(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	i := strings.Index(v, "://")
	if i < 0 {
		// 不做复杂解析，直接整体脱敏。
		return "<redacted>"
	}
	j := strings.Index(v[i+3:], "@")
	if j < 0 {
		return v
	}
	j += i + 3
	return v[:i+3] + "<redacted>@" + v[j+1:]
}
