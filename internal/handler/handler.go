package handler

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"mailapi/internal/auth"
	"mailapi/internal/cache"
	"mailapi/internal/middleware"
	"mailapi/internal/prefix"
	"mailapi/internal/storage"
	"mailapi/internal/store"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	store      store.Interface
	cache      cache.Interface
	storage    storage.Interface
	auth       *auth.Auth
	accountTTL time.Duration
	apiKeys    map[string]*middleware.APIKeyInfo
	prefix     *prefix.Generator
	globalRPM  int64
	bcryptSem  chan struct{}
	// storageCleanupSem 用于限制 API 触发的对象存储清理并发，避免在高并发删除场景下 goroutine 与网络请求爆炸。
	storageCleanupSem chan struct{}

	// allowMessageKeep 控制是否允许通过 API 请求设置 message.keep（长期保留）。
	// 默认 false（仅按 TTL 自动过期），由环境变量在启动时注入。
	allowMessageKeep bool

	// sseHub 用于把 Redis Pub/Sub 的订阅连接从“每 SSE 连接 1 个”降到“每 account 1 个（进程内 fan-out）”。
	sseHub *sseHub

	domainCacheMu sync.Mutex
	domainCache   atomic.Pointer[domainCacheEntry]
}

func New(s store.Interface, c cache.Interface, st storage.Interface, a *auth.Auth, accountTTL time.Duration, apiKeys map[string]*middleware.APIKeyInfo, pg *prefix.Generator, globalRPM int64, maxConcurrentBcrypt int, allowMessageKeep bool) *Handler {
	rpm := globalRPM
	// 兼容旧语义：global=0 表示默认 100；global<0 表示禁用（用于极限压测/单机部署降 Redis 压力）。
	if rpm == 0 {
		rpm = 100
	} else if rpm < 0 {
		rpm = 0
	}

	maxBcrypt := maxConcurrentBcrypt
	if maxBcrypt <= 0 {
		maxBcrypt = runtime.GOMAXPROCS(0)
	}
	if maxBcrypt < 1 {
		maxBcrypt = 1
	}

	// 对象存储删除通常是网络 IO，适当放大并发；但仍需上限，避免高并发删除时把 CPU/FD/带宽打爆。
	maxCleanup := runtime.GOMAXPROCS(0) * 4
	if maxCleanup < 4 {
		maxCleanup = 4
	}
	if maxCleanup > 64 {
		maxCleanup = 64
	}

	return &Handler{
		store:             s,
		cache:             c,
		storage:           st,
		auth:              a,
		accountTTL:        accountTTL,
		apiKeys:           apiKeys,
		prefix:            pg,
		globalRPM:         rpm,
		bcryptSem:         make(chan struct{}, maxBcrypt),
		storageCleanupSem: make(chan struct{}, maxCleanup),
		allowMessageKeep:  allowMessageKeep,
		sseHub:            newSSEHub(c),
	}
}

func (h *Handler) SetupRoutes(r *gin.Engine) {
	hasAPIKeys := len(h.apiKeys) > 0

	// Global rate limit per IP
	r.Use(middleware.RateLimit(h.cache, h.globalRPM, time.Minute))

	// Unified Bearer auth: parses sk_/dk_ API keys and JWT tokens
	r.Use(middleware.BearerAuth(h.apiKeys, h.auth))

	// Per-API-key rate limiting
	r.Use(middleware.APIKeyRateLimit(h.cache))

	// Public routes (API key or JWT accepted for domain scoping)
	r.GET("/domains", h.ListDomains)
	r.POST("/accounts", middleware.RequireAPIKey(hasAPIKeys), h.CreateAccount)
	r.POST("/token", h.CreateToken)
	r.GET("/addresses/random", middleware.RequireAPIKey(hasAPIKeys), h.RandomAddress)

	// Authenticated routes (JWT required)
	authed := r.Group("/", middleware.AuthRequired(), middleware.DomainScopeCheck())
	{
		authed.GET("/me", h.GetMe)
		authed.GET("/accounts/:id", h.GetAccount)
		authed.DELETE("/accounts/:id", h.DeleteAccount)

		authed.GET("/messages", h.ListMessages)
		authed.PATCH("/messages", h.BulkUpdateMessages)
		authed.DELETE("/messages", h.DeleteMessages)
		authed.POST("/messages/bulk-delete", h.BulkDeleteMessagesByIDs)
		authed.GET("/messages/:id", h.GetMessage)
		authed.PATCH("/messages/:id", h.UpdateMessage)
		authed.DELETE("/messages/:id", h.DeleteMessage)
		authed.GET("/messages/:id/download", h.DownloadMessage)
		authed.GET("/messages/:id/attachments/:attachmentId", h.DownloadAttachment)

		authed.GET("/sse", h.SSE)
	}
}
