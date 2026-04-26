package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mailapi/internal/auth"
	"mailapi/internal/cache"
	"mailapi/internal/config"
	"mailapi/internal/debugserver"
	"mailapi/internal/dialect"
	"mailapi/internal/dialect/cfworker"
	"mailapi/internal/dialect/yyds"
	"mailapi/internal/handler"
	"mailapi/internal/health"
	"mailapi/internal/middleware"
	"mailapi/internal/model"
	"mailapi/internal/prefix"
	"mailapi/internal/storage"
	"mailapi/internal/store"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

func main() {
	cfgPathFlag := flag.String("config", "config.yaml", "配置文件路径（也可作为位置参数传入）")
	checkCfg := flag.Bool("check-config", false, "检查配置文件并退出")
	printEffectiveCfg := flag.Bool("print-effective-config", false, "打印生效配置并退出（默认脱敏）")
	full := flag.Bool("full", false, "与 --print-effective-config 搭配：输出未脱敏配置")
	debugAddrOverride := flag.String("debug-addr", "", "覆盖 debug server 监听地址(host:port)，并隐式启用（仅影响 api.debug）")
	flag.Parse()

	cfgPath := *cfgPathFlag
	if args := flag.Args(); len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		cfgPath = args[0]
	}

	if *checkCfg || *printEffectiveCfg {
		fi, err := os.Stat(cfgPath)
		if err != nil {
			log.Fatalf("Config file not found: %v", err)
		}
		if fi.IsDir() {
			log.Fatalf("Config path is a directory: %s", cfgPath)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 支持用环境变量与 flag 覆盖 api debug server 配置（仅影响 debug server，本质是运维便捷项）。
	applyDebugEnvOverrides("MAILAPI_API", &cfg.Server.API.Debug)
	applyDebugAddrOverride(*debugAddrOverride, &cfg.Server.API.Debug)

	if *checkCfg || *printEffectiveCfg {
		if err := config.ValidateAPIConfig(cfg); err != nil {
			log.Fatalf("Invalid config: %v", err)
		}

		if *checkCfg {
			log.Printf("Config OK: %s", cfgPath)
			return
		}

		out := cfg
		if !*full {
			out = config.Redact(cfg)
		}
		b, err := yaml.Marshal(out)
		if err != nil {
			log.Fatalf("Failed to marshal config: %v", err)
		}
		_, _ = os.Stdout.Write(b)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Initialize dependencies
	st, err := store.New(ctx, store.MongoConfig{
		URI:                    cfg.MongoDB.URI,
		Database:               cfg.MongoDB.Database,
		MaxPoolSize:            cfg.MongoDB.MaxPoolSize,
		MinPoolSize:            cfg.MongoDB.MinPoolSize,
		MaxConnecting:          cfg.MongoDB.MaxConnecting,
		ConnectTimeout:         cfg.MongoDB.ConnectTimeout,
		ServerSelectionTimeout: cfg.MongoDB.ServerSelectionTimeout,
		MaxConnIdleTime:        cfg.MongoDB.MaxConnIdleTime,
		AppName:                cfg.MongoDB.AppName,
	}, cfg.Account.TTL, cfg.Message.TTL)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer st.Close(context.Background())

	ca, err := cache.New(ctx, cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer ca.Close()

	sg, err := storage.New(ctx, cfg.MinIO.Endpoint, cfg.MinIO.AccessKey, cfg.MinIO.SecretKey, cfg.MinIO.Bucket, cfg.MinIO.UseSSL)
	if err != nil {
		log.Fatalf("Failed to connect to MinIO: %v", err)
	}

	// Sync domains from config to MongoDB
	if len(cfg.Domains) > 0 {
		domains := make([]model.Domain, 0, len(cfg.Domains))
		for _, d := range cfg.Domains {
			name := strings.ToLower(strings.TrimSpace(d.Domain))
			if name == "" {
				continue
			}
			domains = append(domains, model.Domain{
				Domain:    name,
				IsActive:  d.IsActive,
				IsPrivate: d.IsPrivate,
			})
		}
		if err := st.SyncDomains(ctx, domains); err != nil {
			log.Fatalf("Failed to sync domains: %v", err)
		}
		log.Printf("Synced %d domains from config", len(domains))
	}

	// Build API key info map (default: sk_ prefix; dk_ also accepted)
	apiKeys := make(map[string]*middleware.APIKeyInfo)
	for _, ak := range cfg.APIKeys {
		if !(strings.HasPrefix(ak.Key, "sk_") || strings.HasPrefix(ak.Key, "dk_")) {
			log.Printf("WARNING: API key %q does not use sk_ or dk_ prefix. Consider using sk_ as the default prefix.", ak.Name)
		}

		// 归一化 domains 与 domainLimits：避免大小写/空格导致“看似配置了却不生效”。
		// 约定：域名匹配大小写不敏感，统一转小写。
		wildcard := false
		explicitDomains := make([]string, 0, len(ak.Domains))
		for _, d := range ak.Domains {
			dd := strings.ToLower(strings.TrimSpace(d))
			if dd == "" {
				continue
			}
			if dd == "*" {
				wildcard = true
				continue
			}
			explicitDomains = append(explicitDomains, dd)
		}

		var domainSet map[string]struct{}
		if len(explicitDomains) > 0 {
			domainSet = make(map[string]struct{}, len(explicitDomains))
			for _, d := range explicitDomains {
				domainSet[d] = struct{}{}
			}
		}

		// 兼容旧语义：BearerAuth 往 ctx 里放的 allowed-domains 仍只用 "*" 表示 wildcard。
		// 同时保留 DomainSet 作为“显式授权域名集合”，用于私有域名等需要显式授权的场景。
		domains := explicitDomains
		if wildcard {
			domains = []string{"*"}
		}

		var domainLimits map[string]int64
		if len(ak.DomainLimits) > 0 {
			domainLimits = make(map[string]int64, len(ak.DomainLimits))
			for k, v := range ak.DomainLimits {
				kk := strings.ToLower(strings.TrimSpace(k))
				if kk == "" {
					continue
				}
				domainLimits[kk] = v
			}
			if len(domainLimits) == 0 {
				domainLimits = nil
			}
		}

		defaultDomain := strings.ToLower(strings.TrimSpace(ak.DefaultDomain))
		defaultSubdomain := strings.ToLower(strings.TrimSpace(ak.DefaultSubdomain))

		apiKeys[ak.Key] = &middleware.APIKeyInfo{
			Name:             ak.Name,
			Domains:          domains,
			DefaultDomain:    defaultDomain,
			DefaultSubdomain: defaultSubdomain,
			DomainSet:        domainSet,
			Wildcard:         wildcard,
			RPMLimit:         ak.RPMLimit,
			DomainLimits:     domainLimits,
		}
	}
	if len(apiKeys) > 0 {
		log.Printf("Loaded %d API keys", len(apiKeys))
	}

	au := auth.New(cfg.JWT.Secret, cfg.JWT.Expiry)
	pg := prefix.New()

	// Setup Gin
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	// 注意：不配置 trustedProxies 时，Gin 对 ClientIP 的解析可能受 X-Forwarded-For 影响。
	// 这里保持向后兼容：仅当用户显式配置时才覆盖 Gin 的默认行为。
	if len(cfg.Server.API.TrustedProxies) > 0 {
		if err := r.SetTrustedProxies(cfg.Server.API.TrustedProxies); err != nil {
			log.Fatalf("Invalid server.api.trustedProxies: %v", err)
		}
	}
	if cfg.Server.API.AccessLog.Enabled {
		r.Use(middleware.AccessLog(cfg.Server.API.AccessLog.SampleEvery, cfg.Server.API.AccessLog.SlowThreshold, cfg.Server.API.AccessLog.ErrorsOnly))
	}

	// message.keep（长期保留）是否允许：默认不允许，通过环境变量显式开启。
	allowMessageKeep := false
	if v, ok := envBool("MAILAPI_API_ALLOW_MESSAGE_KEEP"); ok {
		allowMessageKeep = v
	} else if v, ok := envBool("MAILAPI_ALLOW_MESSAGE_KEEP"); ok {
		allowMessageKeep = v
	}

	h := handler.New(st, ca, sg, au, cfg.Account.TTL, apiKeys, pg, cfg.RateLimit.Global, cfg.Server.API.MaxConcurrentBcrypt, allowMessageKeep)
	h.SetupRoutes(r)

	// 可选 debug HTTP server：/healthz /readyz /metrics /debug/pprof
	var debugSrv *http.Server
	if cfg.Server.API.Debug.Enabled {
		s, err := debugserver.New(cfg.Server.API.Debug, health.ReadinessDeps{
			Store:   st,
			Cache:   ca,
			Storage: sg,
		})
		if err != nil {
			log.Fatalf("Invalid server.api.debug: %v", err)
		}
		debugSrv = s

		go func() {
			log.Printf("Debug server starting on %s", debugSrv.Addr)
			if err := debugSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Debug server error: %v", err)
			}
		}()
	}

	// 默认 API 风格（duck）使用现有 gin.Engine；cfworker 风格通过代理转发到上游 Worker。
	var rootHandler http.Handler = r
	if strings.TrimSpace(cfg.Server.API.BaseHost) != "" {
		d := dialect.NewDispatcher(cfg.Server.API.BaseHost, cfg.Server.API.DefaultDialect, cfg.Server.API.EnabledDialects, cfg.Server.API.UnknownDialect)
		d.Register("duck", r)
		d.Register("cfworker", cfworker.NewProxy(cfworker.Config{
			Upstream: cfg.Dialects.CFWorker.Upstream,
			Timeout:  cfg.Dialects.CFWorker.Timeout,
		}))
		d.Register("yyds", yyds.New(yyds.Config{
			Core:           h,
			Store:          st,
			Cache:          ca,
			Storage:        sg,
			Auth:           au,
			AccountTTL:     cfg.Account.TTL,
			TokenTTL:       cfg.JWT.Expiry,
			APIKeys:        apiKeys,
			Prefix:         pg,
			GlobalRPM:      cfg.RateLimit.Global,
			TrustedProxies: cfg.Server.API.TrustedProxies,
			AccessLog:      cfg.Server.API.AccessLog,
			Public:         cfg.Dialects.YYDS,
		}))
		rootHandler = d
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.API.Host, cfg.Server.API.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           rootHandler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// 注意：本服务包含 SSE/下载等流式响应，net/http 的 WriteTimeout 会对“整个响应生命周期”施加写入截止时间，
		// 会导致长连接在超时后被强制截断，因此这里必须禁用（0）。建议由上游反向代理/网关做超时与限速控制。
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		log.Printf("API server starting on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("API server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down API server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if debugSrv != nil {
		_ = debugSrv.Shutdown(shutdownCtx)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("API server forced to shutdown: %v", err)
	}
	log.Println("API server stopped")
}

func applyDebugEnvOverrides(prefix string, dbg *config.DebugServerConfig) {
	if dbg == nil {
		return
	}

	// Addr override: e.g. MAILAPI_API_DEBUG_ADDR=127.0.0.1:6060
	if addr := strings.TrimSpace(os.Getenv(prefix + "_DEBUG_ADDR")); addr != "" {
		host, portStr, err := net.SplitHostPort(addr)
		if err == nil {
			if p, err := strconv.Atoi(portStr); err == nil {
				dbg.Enabled = true
				if strings.TrimSpace(host) != "" {
					dbg.Host = host
				}
				dbg.Port = p
			}
		}
	}

	if v, ok := envBool(prefix + "_DEBUG_ENABLED"); ok {
		dbg.Enabled = v
	}
	if host := strings.TrimSpace(os.Getenv(prefix + "_DEBUG_HOST")); host != "" {
		dbg.Host = host
	}
	if portStr := strings.TrimSpace(os.Getenv(prefix + "_DEBUG_PORT")); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			dbg.Port = p
		}
	}
	if v, ok := envBool(prefix + "_DEBUG_METRICS"); ok {
		dbg.Metrics = v
	}
	if v, ok := envBool(prefix + "_DEBUG_PPROF"); ok {
		dbg.Pprof = v
	}
}

func applyDebugAddrOverride(addr string, dbg *config.DebugServerConfig) {
	addr = strings.TrimSpace(addr)
	if addr == "" || dbg == nil {
		return
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return
	}
	dbg.Enabled = true
	if strings.TrimSpace(host) != "" {
		dbg.Host = host
	}
	dbg.Port = p
}

func envBool(key string) (bool, bool) {
	s := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch s {
	case "1", "true", "yes", "y", "on":
		return true, true
	case "0", "false", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}
