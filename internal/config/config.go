package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Domains   []DomainConfig  `yaml:"domains"`
	APIKeys   []APIKeyConfig  `yaml:"apiKeys"`
	RateLimit RateLimitConfig `yaml:"rateLimit"`
	Server    ServerConfig    `yaml:"server"`
	Dialects  DialectsConfig  `yaml:"dialects"`
	MongoDB   MongoDBConfig   `yaml:"mongodb"`
	Redis     RedisConfig     `yaml:"redis"`
	NATS      NATSConfig      `yaml:"nats"`
	MinIO     MinIOConfig     `yaml:"minio"`
	JWT       JWTConfig       `yaml:"jwt"`
	Account   AccountConfig   `yaml:"account"`
	Message   MessageConfig   `yaml:"message"`
}

// DomainConfig defines an email domain and its SMTP listener bindings.
type DomainConfig struct {
	Domain    string   `yaml:"domain"`
	IsActive  bool     `yaml:"isActive"`
	IsPrivate bool     `yaml:"isPrivate"`
	IPs       []string `yaml:"ips"` // SMTP listener IPs; empty = 0.0.0.0
}

// APIKeyConfig defines an API key with domain-level access control and
// per-key rate limiting. Keys default to the sk_ prefix; dk_ is also accepted.
type APIKeyConfig struct {
	Key          string           `yaml:"key"`
	Name         string           `yaml:"name"`
	Domains      []string         `yaml:"domains"`                // ["*"] for all domains
	RPMLimit     int64            `yaml:"rpmLimit,omitempty"`     // per-key RPM limit (0 = unlimited)
	DomainLimits map[string]int64 `yaml:"domainLimits,omitempty"` // per-key-per-domain RPM overrides
}

// RateLimitConfig defines global rate limiting.
type RateLimitConfig struct {
	Global int64 `yaml:"global"` // global RPM per IP (0 = default 100; <0 = disabled)
}

type ServerConfig struct {
	API    APIServerConfig    `yaml:"api"`
	SMTP   SMTPServerConfig   `yaml:"smtp"`
	Worker WorkerServerConfig `yaml:"worker"`
}

// DebugServerConfig 定义独立的 debug HTTP server（健康检查/指标/pprof）。
// 注意：debug server 默认关闭，且强烈建议仅绑定到 127.0.0.1/::1，避免暴露运行时信息到公网。
type DebugServerConfig struct {
	Enabled bool   `yaml:"enabled"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	Metrics bool   `yaml:"metrics"`
	Pprof   bool   `yaml:"pprof"`
}

type WorkerServerConfig struct {
	// Worker 没有业务监听端口，但可以开启 debug HTTP server 用于 ready/metrics/pprof。
	Debug DebugServerConfig `yaml:"debug"`
}

type APIServerConfig struct {
	Host      string          `yaml:"host"`
	Port      int             `yaml:"port"`
	AccessLog AccessLogConfig `yaml:"accessLog"`
	// MaxConcurrentBcrypt 限制同一时刻允许执行的 bcrypt 操作数量（创建账号/登录会用到）。
	// 0 表示自动（按 GOMAXPROCS 取值）。
	MaxConcurrentBcrypt int `yaml:"maxConcurrentBcrypt"`

	// TrustedProxies 用于配置 Gin 的可信代理列表，影响 c.ClientIP() 对 X-Forwarded-For 等头的解析。
	// 为空表示不覆盖 Gin 默认行为（保持向后兼容）。生产环境强烈建议显式配置可信代理，避免被伪造 XFF 绕过 IP 限流。
	TrustedProxies []string `yaml:"trustedProxies"`

	// Debug HTTP server（健康检查/指标/pprof）。默认关闭；建议仅绑定 127.0.0.1 并置于防火墙/LB 后。
	Debug DebugServerConfig `yaml:"debug"`

	// --- 多 API 风格（dialect）路由（按 Host 子域前缀）---
	// BaseHost 用于从 Host 中解析 `<dialect>.<baseHost>`，例如 `cfworker.api.mailapi.com`。
	// 为空表示禁用该功能（保持向后兼容：所有请求使用单一默认风格）。
	BaseHost string `yaml:"baseHost"`
	// DefaultDialect 用于 `baseHost` 本身（如 `api.mailapi.com`）以及非匹配 baseHost 的 Host（如 localhost/IP）时的默认风格。
	// 建议默认 `duck`，保持现有客户端与文档不受影响。
	DefaultDialect string `yaml:"defaultDialect"`
	// EnabledDialects 用于限制允许路由到哪些 dialect。为空表示不限制（允许所有已注册 dialect）。
	EnabledDialects []string `yaml:"enabledDialects"`
	// UnknownDialect 定义未知 dialect 的处理策略：
	// - "reject"：拒绝（建议生产环境默认）
	// - "fallback"：回退到 DefaultDialect（用于灰度期容错）
	UnknownDialect string `yaml:"unknownDialect"`
}

// AccessLogConfig 控制 API 访问日志（高并发下建议开启采样或仅记录慢请求/错误）。
type AccessLogConfig struct {
	Enabled       bool          `yaml:"enabled"`
	SampleEvery   int64         `yaml:"sampleEvery"`   // 1=全量；100=百分之一；0/负数按 1 处理
	SlowThreshold time.Duration `yaml:"slowThreshold"` // 例如 500ms；<=0 表示不按慢请求强制记录
	ErrorsOnly    bool          `yaml:"errorsOnly"`    // true=仅记录 status>=400 的请求
}

type SMTPServerConfig struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	Domain          string        `yaml:"domain"`
	MaxMessageBytes int64         `yaml:"maxMessageBytes"`
	MaxRecipients   int           `yaml:"maxRecipients"`
	ReadTimeout     time.Duration `yaml:"readTimeout"`
	WriteTimeout    time.Duration `yaml:"writeTimeout"`

	// TLS/STARTTLS 配置（可选）。默认关闭（纯明文，保持向后兼容）。
	TLS SMTPTLSConfig `yaml:"tls"`

	// Debug HTTP server（健康检查/指标/pprof）。默认关闭；建议仅绑定 127.0.0.1。
	Debug DebugServerConfig `yaml:"debug"`
}

type SMTPTLSConfig struct {
	// Enabled=true 时启用 STARTTLS（对 EHLO 广播 STARTTLS，并接受 STARTTLS 升级）。
	Enabled bool `yaml:"enabled"`
	// CertFile/KeyFile 为 PEM 格式证书与私钥路径。
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`
	// RequireTLS=true 时要求客户端在发送 MAIL/RCPT/DATA 前先 STARTTLS（否则返回 530）。
	// 注意：这会影响可达性（部分发件方不支持 STARTTLS）；默认 false。
	RequireTLS bool `yaml:"requireTLS"`
	// MinVersion 可选：tls 最低版本（如 1.2/1.3）。为空表示默认 1.2。
	MinVersion string `yaml:"minVersion"`
}

// DialectsConfig defines per-dialect settings (optional).
type DialectsConfig struct {
	CFWorker CFWorkerDialectConfig `yaml:"cfworker"`
	YYDS     YYDSDialectConfig     `yaml:"yyds"`
}

// CFWorkerDialectConfig defines cfworker (cloudflare_temp_email style) settings.
type CFWorkerDialectConfig struct {
	// Upstream is the cfworker base URL. Empty = not configured (requests will return 503).
	Upstream string `yaml:"upstream"`
	// Timeout is the per-request timeout for proxying to upstream. 0 = no extra timeout.
	Timeout time.Duration `yaml:"timeout"`
}

// YYDSDialectConfig defines the optional public metadata exposed by the yyds dialect.
// 临时邮箱/消息接口本身不依赖这些字段；它们仅用于 `/v1/plans`、`/v1/pricing`、`/v1/domain-reward/config`
// 与 `/v1/stats` 这类“公开信息端点”。
type YYDSDialectConfig struct {
	// PublicBaseURL 可选：用于 `/v1/llms.txt` 中展示公开 Base URL。
	// 为空时将根据当前请求的 scheme + host 动态推导。
	PublicBaseURL string `yaml:"publicBaseURL"`

	Plans        []YYDSPlanConfig       `yaml:"plans"`
	Pricing      YYDSPricingConfig      `yaml:"pricing"`
	DomainReward YYDSDomainRewardConfig `yaml:"domainReward"`
	Stats        YYDSStatsConfig        `yaml:"stats"`
}

type YYDSPlanConfig struct {
	ID                 string   `yaml:"id" json:"id"`
	Name               string   `yaml:"name" json:"name"`
	Description        string   `yaml:"description" json:"description"`
	PriceMonthly       float64  `yaml:"priceMonthly" json:"priceMonthly"`
	MaxDomains         int64    `yaml:"maxDomains" json:"maxDomains"`
	MaxInboxes         int64    `yaml:"maxInboxes" json:"maxInboxes"`
	MaxAPIKeys         int64    `yaml:"maxApiKeys" json:"maxApiKeys"`
	MaxSubdomainDepth  int64    `yaml:"maxSubdomainDepth" json:"maxSubdomainDepth"`
	MaxMessagesPerDay  int64    `yaml:"maxMessagesPerDay" json:"maxMessagesPerDay"`
	MaxAPICallsDaily   int64    `yaml:"maxApiCallsDaily" json:"maxApiCallsDaily"`
	MaxAPICallsWeekly  int64    `yaml:"maxApiCallsWeekly" json:"maxApiCallsWeekly"`
	MaxAPICallsMonthly int64    `yaml:"maxApiCallsMonthly" json:"maxApiCallsMonthly"`
	MaxWebhooks        int64    `yaml:"maxWebhooks" json:"maxWebhooks"`
	MaxWildcardRules   int64    `yaml:"maxWildcardRules" json:"maxWildcardRules"`
	RetentionDays      int64    `yaml:"retentionDays" json:"retentionDays"`
	MaxAttachmentBytes int64    `yaml:"maxAttachmentBytes" json:"maxAttachmentBytes"`
	StorageBytes       int64    `yaml:"storageBytes" json:"storageBytes"`
	MaxRPS             int64    `yaml:"maxRps" json:"maxRps"`
	Features           []string `yaml:"features" json:"features"`
	SortOrder          int64    `yaml:"sortOrder" json:"sortOrder"`
	IsActive           bool     `yaml:"isActive" json:"isActive"`
}

type YYDSPricingConfig struct {
	Currency   YYDSPricingCurrencyConfig    `yaml:"currency" json:"currency"`
	Packages   []YYDSPricingPackageConfig   `yaml:"packages" json:"packages"`
	RateLimits []YYDSPricingRateLimitConfig `yaml:"rateLimits" json:"rateLimits"`
}

type YYDSPricingCurrencyConfig struct {
	Code   string `yaml:"code" json:"code"`
	Suffix string `yaml:"suffix" json:"suffix"`
	Symbol string `yaml:"symbol" json:"symbol"`
}

type YYDSPricingPackageConfig struct {
	Quantity   int64 `yaml:"quantity" json:"quantity"`
	PriceCents int64 `yaml:"priceCents" json:"priceCents"`
}

type YYDSPricingRateLimitConfig struct {
	Tier        string `yaml:"tier" json:"tier"`
	DisplayName string `yaml:"displayName" json:"displayName"`
	MaxDaily    int64  `yaml:"maxDaily" json:"maxDaily"`
	RPS         int64  `yaml:"rps" json:"rps"`
	Burst       int64  `yaml:"burst" json:"burst"`
}

type YYDSDomainRewardConfig struct {
	CreditExpireDays int64 `yaml:"creditExpireDays" json:"creditExpireDays"`
	CreditsPerCycle  int64 `yaml:"creditsPerCycle" json:"creditsPerCycle"`
	RunHour          int64 `yaml:"runHour" json:"runHour"`
	UsagePerCredit   int64 `yaml:"usagePerCredit" json:"usagePerCredit"`
}

type YYDSStatsConfig struct {
	TotalUsers              int64                  `yaml:"totalUsers" json:"totalUsers"`
	TotalDomains            int64                  `yaml:"totalDomains" json:"totalDomains"`
	VerifiedDomains         int64                  `yaml:"verifiedDomains" json:"verifiedDomains"`
	PublicDomains           int64                  `yaml:"publicDomains" json:"publicDomains"`
	TotalInboxes            int64                  `yaml:"totalInboxes" json:"totalInboxes"`
	AnonInboxes             int64                  `yaml:"anonInboxes" json:"anonInboxes"`
	TotalStoredMessages     int64                  `yaml:"totalStoredMessages" json:"totalStoredMessages"`
	TotalHistoricalMessages int64                  `yaml:"totalHistoricalMessages" json:"totalHistoricalMessages"`
	TotalCreatedInboxes     int64                  `yaml:"totalCreatedInboxes" json:"totalCreatedInboxes"`
	TotalMessages           int64                  `yaml:"totalMessages" json:"totalMessages"`
	TodayAPICalls           int64                  `yaml:"todayApiCalls" json:"todayApiCalls"`
	TopDomains              []YYDSTopDomainConfig  `yaml:"topDomains" json:"topDomains"`
	HourlyActivity          []YYDSHourlyStatConfig `yaml:"hourlyActivity" json:"hourlyActivity"`
	DailyTrend              []YYDSDailyTrendConfig `yaml:"dailyTrend" json:"dailyTrend"`
}

type YYDSTopDomainConfig struct {
	Domain     string `yaml:"domain" json:"domain"`
	IsVerified bool   `yaml:"isVerified" json:"isVerified"`
	UsageToday int64  `yaml:"usageToday" json:"usageToday"`
	UsageTotal int64  `yaml:"usageTotal" json:"usageTotal"`
}

type YYDSHourlyStatConfig struct {
	Hour     string `yaml:"hour" json:"hour"`
	Inboxes  int64  `yaml:"inboxes" json:"inboxes"`
	APICalls int64  `yaml:"apiCalls" json:"apiCalls"`
}

type YYDSDailyTrendConfig struct {
	Date     string `yaml:"date" json:"date"`
	Inboxes  int64  `yaml:"inboxes" json:"inboxes"`
	APICalls int64  `yaml:"apiCalls" json:"apiCalls"`
	Users    int64  `yaml:"users" json:"users"`
}

type MongoDBConfig struct {
	URI      string `yaml:"uri"`
	Database string `yaml:"database"`

	// --- 客户端侧调参（可选）---
	// maxPoolSize:
	//   <0: 不覆盖驱动默认值
	//    0: 自动（按 CPU 规模估算，适合高并发）
	//   >0: 固定值
	MaxPoolSize int64 `yaml:"maxPoolSize"`
	// minPoolSize:
	//   <0: 不覆盖驱动默认值
	//   >=0: 固定值（通常 0 即可）
	MinPoolSize int64 `yaml:"minPoolSize"`
	// maxConnecting:
	//   <0: 不覆盖驱动默认值
	//    0: 自动
	//   >0: 固定值
	MaxConnecting int64 `yaml:"maxConnecting"`

	// 连接/选择节点相关超时（0 表示使用默认值）
	ConnectTimeout         time.Duration `yaml:"connectTimeout"`
	ServerSelectionTimeout time.Duration `yaml:"serverSelectionTimeout"`

	// 连接空闲多久后允许驱动主动关闭（<=0 表示不设置，使用驱动默认）
	MaxConnIdleTime time.Duration `yaml:"maxConnIdleTime"`

	// 应用名（会出现在 Mongo 的连接列表中，便于运维与观测）
	AppName string `yaml:"appName"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type NATSConfig struct {
	URL     string `yaml:"url"`
	Stream  string `yaml:"stream"`
	Subject string `yaml:"subject"`

	// ConsumerAckWait/ConsumerMaxDeliver 用于 Worker 消费端（JetStream consumer）的关键参数。
	// SMTP 侧不会使用这些字段，但为了统一配置仍放在 nats 段。
	ConsumerAckWait    time.Duration `yaml:"consumerAckWait"`
	ConsumerMaxDeliver int           `yaml:"consumerMaxDeliver"`
}

type MinIOConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"accessKey"`
	SecretKey string `yaml:"secretKey"`
	Bucket    string `yaml:"bucket"`
	UseSSL    bool   `yaml:"useSSL"`
}

type JWTConfig struct {
	Secret string        `yaml:"secret"`
	Expiry time.Duration `yaml:"expiry"`
}

type AccountConfig struct {
	TTL time.Duration `yaml:"ttl"`
}

type MessageConfig struct {
	TTL time.Duration `yaml:"ttl"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			API: APIServerConfig{
				Host: "0.0.0.0",
				Port: 8080,
				AccessLog: AccessLogConfig{
					Enabled:       true,
					SampleEvery:   100, // 高并发下默认采样，避免访问日志把 CPU/磁盘打爆
					SlowThreshold: 500 * time.Millisecond,
					ErrorsOnly:    false,
				},
				MaxConcurrentBcrypt: 0,
				Debug: DebugServerConfig{
					Enabled: false,
					Host:    "127.0.0.1",
					Port:    6060,
					Metrics: true,
					Pprof:   false,
				},
				BaseHost:        "",
				DefaultDialect:  "duck",
				EnabledDialects: nil,
				UnknownDialect:  "reject",
			},
			SMTP: SMTPServerConfig{
				Host:            "0.0.0.0",
				Port:            25,
				Domain:          "mail.example.com",
				MaxMessageBytes: 20 << 20,
				MaxRecipients:   50,
				ReadTimeout:     60 * time.Second,
				WriteTimeout:    60 * time.Second,
				TLS: SMTPTLSConfig{
					Enabled:    false,
					CertFile:   "",
					KeyFile:    "",
					RequireTLS: false,
					MinVersion: "",
				},
				Debug: DebugServerConfig{
					Enabled: false,
					Host:    "127.0.0.1",
					Port:    6061,
					Metrics: true,
					Pprof:   false,
				},
			},
			Worker: WorkerServerConfig{
				Debug: DebugServerConfig{
					Enabled: false,
					Host:    "127.0.0.1",
					Port:    6062,
					Metrics: true,
					Pprof:   false,
				},
			},
		},
		Dialects: DialectsConfig{
			CFWorker: CFWorkerDialectConfig{
				Upstream: "",
				Timeout:  15 * time.Second,
			},
			YYDS: YYDSDialectConfig{
				Plans: []YYDSPlanConfig{},
				Pricing: YYDSPricingConfig{
					Currency: YYDSPricingCurrencyConfig{
						Code:   "CNY",
						Symbol: "¥",
					},
					Packages:   []YYDSPricingPackageConfig{},
					RateLimits: []YYDSPricingRateLimitConfig{},
				},
				Stats: YYDSStatsConfig{
					TopDomains:     []YYDSTopDomainConfig{},
					HourlyActivity: []YYDSHourlyStatConfig{},
					DailyTrend:     []YYDSDailyTrendConfig{},
				},
			},
		},
		MongoDB: MongoDBConfig{
			URI:                    "mongodb://localhost:27017",
			Database:               "mailapi",
			MaxPoolSize:            0, // 0=自动（见 store.effectiveMaxPoolSize）
			MinPoolSize:            -1,
			MaxConnecting:          0,
			ConnectTimeout:         10 * time.Second,
			ServerSelectionTimeout: 10 * time.Second,
			MaxConnIdleTime:        5 * time.Minute,
			AppName:                "mailapi",
		},
		Redis: RedisConfig{Addr: "localhost:6379", DB: 0},
		NATS: NATSConfig{
			URL:                "nats://localhost:4222",
			Stream:             "EMAILS",
			Subject:            "emails.incoming",
			ConsumerAckWait:    30 * time.Second,
			ConsumerMaxDeliver: 3,
		},
		MinIO:   MinIOConfig{Endpoint: "localhost:9000", AccessKey: "minioadmin", SecretKey: "minioadmin", Bucket: "attachments"},
		JWT:     JWTConfig{Secret: "change-me-to-a-random-secret", Expiry: time.Hour},
		Account: AccountConfig{TTL: 168 * time.Hour},
		Message: MessageConfig{TTL: 168 * time.Hour},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// 仅在“文件不存在”时才静默使用默认值，避免权限/IO 错误被误吞导致带着默认密钥启动。
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// 约定：TLS 证书路径允许写相对路径（相对于 config.yaml 所在目录），便于部署与打包。
	// 注意：这里仅做路径归一化，不校验证书内容；证书可用性在 ValidateSMTPConfig/进程启动阶段检查。
	cfg.Server.SMTP.TLS.CertFile = resolvePathRelativeToConfig(path, cfg.Server.SMTP.TLS.CertFile)
	cfg.Server.SMTP.TLS.KeyFile = resolvePathRelativeToConfig(path, cfg.Server.SMTP.TLS.KeyFile)

	return cfg, nil
}

func resolvePathRelativeToConfig(configPath, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	base := filepath.Dir(configPath)
	if base == "" || base == "." {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}
