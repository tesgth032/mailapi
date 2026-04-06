package main

import (
	"context"
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
	"mailapi/internal/storage"
	"mailapi/internal/store"
	"mailapi/internal/worker"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"gopkg.in/yaml.v3"
)

func main() {
	cfgPathFlag := flag.String("config", "config.yaml", "配置文件路径（也可作为位置参数传入）")
	checkCfg := flag.Bool("check-config", false, "检查配置文件并退出")
	printEffectiveCfg := flag.Bool("print-effective-config", false, "打印生效配置并退出（默认脱敏）")
	full := flag.Bool("full", false, "与 --print-effective-config 搭配：输出未脱敏配置")
	debugAddrOverride := flag.String("debug-addr", "", "覆盖 debug server 监听地址(host:port)，并隐式启用（仅影响 worker.debug）")
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

	// 支持用环境变量与 flag 覆盖 worker debug server 配置（仅影响 debug server，本质是运维便捷项）。
	applyDebugEnvOverrides("MAILAPI_WORKER", &cfg.Server.Worker.Debug)
	applyDebugAddrOverride(*debugAddrOverride, &cfg.Server.Worker.Debug)

	if *checkCfg || *printEffectiveCfg {
		if err := config.ValidateWorkerConfig(cfg); err != nil {
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

	depCtx, depCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer depCancel()

	st, err := store.New(depCtx, store.MongoConfig{
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

	ca, err := cache.New(depCtx, cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer ca.Close()

	q, err := queue.New(depCtx, cfg.NATS.URL, cfg.NATS.Stream, cfg.NATS.Subject, cfg.NATS.ConsumerAckWait, cfg.NATS.ConsumerMaxDeliver)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer q.Close()

	sg, err := storage.New(depCtx, cfg.MinIO.Endpoint, cfg.MinIO.AccessKey, cfg.MinIO.SecretKey, cfg.MinIO.Bucket, cfg.MinIO.UseSSL)
	if err != nil {
		log.Fatalf("Failed to connect to MinIO: %v", err)
	}

	// Separate lightweight NATS connection for readiness checks.
	var ncHealth *nats.Conn
	if cfg.Server.Worker.Debug.Enabled {
		ncHealth, err = nats.Connect(cfg.NATS.URL,
			nats.Name("mailapi-worker-health"),
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
	}

	var debugSrv *http.Server
	if cfg.Server.Worker.Debug.Enabled {
		ready := health.NewReadyzChecks([]health.Check{
			{Name: "mongodb", Check: st.Ping},
			{Name: "redis", Check: ca.Ping},
			{Name: "minio", Check: sg.Health},
			{Name: "nats", Check: func(ctx context.Context) error { return checkNATSStream(ctx, ncHealth, cfg.NATS.Stream) }},
		}, health.Options{})

		s, err := debugserver.NewWithReadyHandler(cfg.Server.Worker.Debug, ready)
		if err != nil {
			log.Fatalf("Invalid server.worker.debug: %v", err)
		}
		debugSrv = s

		go func() {
			log.Printf("Debug server starting on %s", debugSrv.Addr)
			if err := debugSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Debug server error: %v", err)
			}
		}()
	}

	// Start worker (consumes NATS JetStream messages until ctx cancelled).
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	w := worker.New(st, ca, q, sg, cfg.Message.TTL)
	errCh := make(chan error, 1)
	go func() { errCh <- w.Start(runCtx) }()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	workerExited := false
	var workerErr error

	select {
	case <-quit:
		log.Println("Shutting down worker...")
		runCancel()
	case workerErr = <-errCh:
		workerExited = true
		runCancel()
	}

	// Stop debug server
	if debugSrv != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = debugSrv.Shutdown(shutdownCtx)
		shutdownCancel()
	}

	// Wait for worker goroutine to return (best-effort).
	if !workerExited {
		select {
		case workerErr = <-errCh:
		case <-time.After(5 * time.Second):
		}
	}

	if workerErr != nil && !errors.Is(workerErr, context.Canceled) {
		log.Printf("Worker stopped with error: %v", workerErr)
	}

	log.Println("Worker stopped")
}

func applyDebugEnvOverrides(prefix string, dbg *config.DebugServerConfig) {
	if dbg == nil {
		return
	}

	// Addr override: e.g. MAILAPI_WORKER_DEBUG_ADDR=127.0.0.1:6062
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
