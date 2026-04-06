package main

import (
	"context"
	"crypto/tls"
	"errors"
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

	"mailapi/internal/cache"
	"mailapi/internal/config"
	"mailapi/internal/debugserver"
	"mailapi/internal/health"
	"mailapi/internal/queue"
	smtpserver "mailapi/internal/smtp"
	"mailapi/internal/store"

	gosmtp "github.com/emersion/go-smtp"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"gopkg.in/yaml.v3"
)

func main() {
	cfgPathFlag := flag.String("config", "config.yaml", "配置文件路径（也可作为位置参数传入）")
	checkCfg := flag.Bool("check-config", false, "检查配置文件并退出")
	printEffectiveCfg := flag.Bool("print-effective-config", false, "打印生效配置并退出（默认脱敏）")
	full := flag.Bool("full", false, "与 --print-effective-config 搭配：输出未脱敏配置")
	debugAddrOverride := flag.String("debug-addr", "", "覆盖 debug server 监听地址(host:port)，并隐式启用（仅影响 smtp.debug）")
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

	// 支持用环境变量与 flag 覆盖 smtp debug server 配置（仅影响 debug server，本质是运维便捷项）。
	applyDebugEnvOverrides("MAILAPI_SMTP", &cfg.Server.SMTP.Debug)
	applyDebugAddrOverride(*debugAddrOverride, &cfg.Server.SMTP.Debug)

	if *checkCfg || *printEffectiveCfg {
		if err := config.ValidateSMTPConfig(cfg); err != nil {
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

	// Optional: STARTTLS (RFC 3207)
	var tlsConfig *tls.Config
	if cfg.Server.SMTP.TLS.Enabled {
		cert, err := tls.LoadX509KeyPair(cfg.Server.SMTP.TLS.CertFile, cfg.Server.SMTP.TLS.KeyFile)
		if err != nil {
			log.Fatalf("Failed to load SMTP TLS cert/key: %v", err)
		}
		minVer := uint16(tls.VersionTLS12)
		if s := strings.TrimSpace(cfg.Server.SMTP.TLS.MinVersion); s != "" {
			if v, ok := config.ParseTLSMinVersion(s); ok {
				minVer = v
			} else {
				log.Fatalf("Invalid server.smtp.tls.minVersion: %q", cfg.Server.SMTP.TLS.MinVersion)
			}
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   minVer,
		}
	}

	// Optional: MongoDB lookup for RCPT TO self-heal when Redis misses (e.g., Redis flush/data loss).
	// 若未配置/未连通，则 SMTP 仍可运行，但地址缓存自愈能力会被禁用（完全依赖 Redis addr:* 缓存）。
	var lookup smtpserver.RecipientLookup
	if strings.TrimSpace(cfg.MongoDB.URI) != "" && strings.TrimSpace(cfg.MongoDB.Database) != "" {
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
			log.Printf("WARNING: failed to connect to MongoDB for recipient lookup; address self-heal disabled: %v", err)
		} else {
			lookup = st
			defer st.Close(context.Background())
		}
	}

	// Initialize Redis cache (for RCPT TO validation and rate limiting)
	ca, err := cache.New(ctx, cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer ca.Close()

	// Initialize NATS queue (for publishing incoming emails)
	q, err := queue.New(ctx, cfg.NATS.URL, cfg.NATS.Stream, cfg.NATS.Subject, cfg.NATS.ConsumerAckWait, cfg.NATS.ConsumerMaxDeliver)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer q.Close()

	var debugSrv *http.Server
	if cfg.Server.SMTP.Debug.Enabled {
		// Separate lightweight NATS connection for readiness checks (keeps /readyz cheap and independent).
		ncHealth, err := nats.Connect(cfg.NATS.URL,
			nats.Name("mailapi-smtp-health"),
			nats.RetryOnFailedConnect(true),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
			nats.Timeout(2*time.Second),
		)
		if err != nil {
			log.Printf("WARNING: failed to create NATS health connection: %v", err)
		}
		if ncHealth != nil {
			defer ncHealth.Close()
		}

		checks := []health.Check{
			{Name: "redis", Check: ca.Ping},
			{Name: "nats", Check: func(ctx context.Context) error { return checkNATSStream(ctx, ncHealth, cfg.NATS.Stream) }},
		}
		// 若启用了 Mongo lookup，则也纳入 readiness（避免“收件自愈依赖未就绪”时误判 ready）。
		if lookup != nil {
			if st, ok := lookup.(interface {
				Ping(ctx context.Context) error
			}); ok {
				checks = append(checks, health.Check{Name: "mongodb", Check: st.Ping})
			}
		}

		ready := health.NewReadyzChecks(checks, health.Options{})

		s, err := debugserver.NewWithReadyHandler(cfg.Server.SMTP.Debug, ready)
		if err != nil {
			log.Fatalf("Invalid server.smtp.debug: %v", err)
		}
		debugSrv = s

		go func() {
			log.Printf("Debug server starting on %s", debugSrv.Addr)
			if err := debugSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Debug server error: %v", err)
			}
		}()
	}

	servers, err := startSMTPServers(cfg, ca, q, lookup, tlsConfig)
	if err != nil {
		log.Fatalf("Failed to start SMTP server(s): %v", err)
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down SMTP server(s)...")
	if debugSrv != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = debugSrv.Shutdown(shutdownCtx)
		shutdownCancel()
	}
	for _, srv := range servers {
		_ = srv.Close()
	}
	log.Println("SMTP server(s) stopped")
}

func startSMTPServers(cfg *config.Config, ca *cache.Cache, q *queue.Queue, lookup smtpserver.RecipientLookup, tlsConfig *tls.Config) ([]*gosmtp.Server, error) {
	if cfg == nil {
		return nil, errors.New("nil config")
	}

	var servers []*gosmtp.Server

	if len(cfg.Domains) > 0 {
		// Multi-listener mode: derive listeners from domain configs
		domainIPs := make(map[string][]string)
		for _, d := range cfg.Domains {
			if d.IsActive {
				domainIPs[d.Domain] = d.IPs
			}
		}

		listeners := smtpserver.BuildListeners(domainIPs, cfg.Server.SMTP.Port)
		if len(listeners) == 0 {
			return nil, fmt.Errorf("no SMTP listeners could be started (no local IPs match domain config)")
		}

		for _, l := range listeners {
			backend := smtpserver.NewBackend(ca, q, lookup, cfg.Account.TTL, cfg.Server.SMTP.TLS.RequireTLS, cfg.Server.SMTP.Domain, l.Domains, cfg.Server.SMTP.MaxMessageBytes)
			srv := smtpserver.NewServer(
				backend, l.Addr, cfg.Server.SMTP.Domain,
				cfg.Server.SMTP.MaxMessageBytes, cfg.Server.SMTP.MaxRecipients,
				cfg.Server.SMTP.ReadTimeout, cfg.Server.SMTP.WriteTimeout, tlsConfig,
			)
			servers = append(servers, srv)

			go func(s *gosmtp.Server, lc smtpserver.ListenerConfig) {
				log.Printf("SMTP listener starting on %s (domains: %v)", lc.Addr, lc.Domains)
				if err := s.ListenAndServe(); err != nil {
					log.Fatalf("SMTP listener %s error: %v", lc.Addr, err)
				}
			}(srv, l)
		}

		log.Printf("Started %d SMTP listener(s)", len(listeners))
		return servers, nil
	}

	// Single-listener mode (backward compatible: no domains in config)
	addr := fmt.Sprintf("%s:%d", cfg.Server.SMTP.Host, cfg.Server.SMTP.Port)
	backend := smtpserver.NewBackend(ca, q, lookup, cfg.Account.TTL, cfg.Server.SMTP.TLS.RequireTLS, cfg.Server.SMTP.Domain, nil, cfg.Server.SMTP.MaxMessageBytes)
	srv := smtpserver.NewServer(
		backend, addr, cfg.Server.SMTP.Domain,
		cfg.Server.SMTP.MaxMessageBytes, cfg.Server.SMTP.MaxRecipients,
		cfg.Server.SMTP.ReadTimeout, cfg.Server.SMTP.WriteTimeout, tlsConfig,
	)
	servers = append(servers, srv)

	go func() {
		log.Printf("SMTP server starting on %s (domain: %s)", addr, cfg.Server.SMTP.Domain)
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalf("SMTP server error: %v", err)
		}
	}()

	return servers, nil
}

func applyDebugEnvOverrides(prefix string, dbg *config.DebugServerConfig) {
	if dbg == nil {
		return
	}

	// Addr override: e.g. MAILAPI_SMTP_DEBUG_ADDR=127.0.0.1:6061
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

func checkNATSStream(ctx context.Context, nc *nats.Conn, stream string) error {
	if nc == nil {
		return errors.New("no nats health connection")
	}
	if nc.Status() != nats.CONNECTED {
		return fmt.Errorf("nats status: %s", nc.Status().String())
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("jetstream: %w", err)
	}
	if _, err := js.Stream(ctx, stream); err != nil {
		return fmt.Errorf("stream %q: %w", stream, err)
	}
	return nil
}
