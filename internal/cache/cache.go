package cache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

type Cache struct {
	client           *redis.Client
	incrExpireScript *redis.Script
	blocked          *blockCache
}

func New(ctx context.Context, addr, password string, db int) (*Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	// INCR + set TTL only on first increment (fixed window).
	// This avoids refreshing TTL on every request and reduces Redis load.
	script := redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
	return current
`)

	// 本地超限缓存：用于“单个 key 被打爆”或“多 key 集合高并发”时，避免在已经超限后仍然
	// 每个请求都去 Redis INCR，显著降低 Redis 压力与本服务 CPU/上下文切换开销。
	blocked := newBlockCache(200000, 30*time.Second)

	return &Cache{client: client, incrExpireScript: script, blocked: blocked}, nil
}

func (c *Cache) Close() error {
	if c.blocked != nil {
		c.blocked.Close()
	}
	return c.client.Close()
}

func (c *Cache) Client() *redis.Client {
	return c.client
}

// Ping 用于 readiness 检查：验证 Redis 可用性（轻量）。
func (c *Cache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// --- Address caching for SMTP RCPT TO validation ---

func addressKey(addr string) string {
	return "addr:" + addr
}

func (c *Cache) SetAddress(ctx context.Context, addr string, ttl time.Duration) error {
	return c.client.Set(ctx, addressKey(addr), "1", ttl).Err()
}

func (c *Cache) HasAddress(ctx context.Context, addr string) (bool, error) {
	val, err := c.client.Exists(ctx, addressKey(addr)).Result()
	if err != nil {
		return false, err
	}
	return val > 0, nil
}

func (c *Cache) RemoveAddress(ctx context.Context, addr string) error {
	return c.client.Del(ctx, addressKey(addr)).Err()
}

// --- Pub/Sub for SSE real-time notifications ---

func eventChannel(accountID string) string {
	return "email:events:" + accountID
}

func (c *Cache) Publish(ctx context.Context, accountID string, payload string) error {
	return c.client.Publish(ctx, eventChannel(accountID), payload).Err()
}

func (c *Cache) Subscribe(ctx context.Context, accountID string) *redis.PubSub {
	return c.client.Subscribe(ctx, eventChannel(accountID))
}

// --- Rate Limiting (fixed window) ---

func rateLimitKey(ip string) string {
	return "rl:" + ip
}

func (c *Cache) incrWithTTL(ctx context.Context, key string, window time.Duration) (int64, error) {
	ttlMs := window.Milliseconds()
	if ttlMs <= 0 {
		ttlMs = 1
	}

	// Lua script is atomic and sets TTL only once per window.
	return c.incrExpireScript.Run(ctx, c.client, []string{key}, ttlMs).Int64()
}

func (c *Cache) isBlocked(now time.Time, key string) bool {
	if c.blocked == nil {
		return false
	}
	return c.blocked.IsBlocked(key, now)
}

func (c *Cache) blockUntilWindowReset(ctx context.Context, now time.Time, key string, window time.Duration) {
	if c.blocked == nil {
		return
	}

	ttl, err := c.client.PTTL(ctx, key).Result()
	if err != nil || ttl <= 0 || ttl > window {
		ttl = window
	}
	c.blocked.Block(key, now.Add(ttl))
}

// CheckRateLimit returns true if the request should be allowed.
func (c *Cache) CheckRateLimit(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	key := rateLimitKey(ip)

	now := time.Now()
	if c.isBlocked(now, key) {
		return false, nil
	}

	current, err := c.incrWithTTL(ctx, key, window)
	if err != nil {
		return false, err
	}

	allowed := current <= limit
	if !allowed {
		c.blockUntilWindowReset(ctx, now, key, window)
	}

	return allowed, nil
}

// --- SMTP Rate Limiting ---

func smtpRateLimitKey(ip string) string {
	return "smtp_rl:" + ip
}

// CheckSMTPRateLimit returns true if the SMTP connection should be allowed.
func (c *Cache) CheckSMTPRateLimit(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	key := smtpRateLimitKey(ip)

	now := time.Now()
	if c.isBlocked(now, key) {
		return false, nil
	}

	current, err := c.incrWithTTL(ctx, key, window)
	if err != nil {
		return false, err
	}

	allowed := current <= limit
	if !allowed {
		c.blockUntilWindowReset(ctx, now, key, window)
	}

	return allowed, nil
}

// --- Per-API-Key Rate Limiting ---

func keyRateLimitKey(apiKey string) string {
	return "rl:key:" + apiKey
}

func keyDomainRateLimitKey(apiKey, domain string) string {
	return "rl:key:" + apiKey + ":d:" + domain
}

// CheckKeyRateLimit returns true if the API key request should be allowed.
func (c *Cache) CheckKeyRateLimit(ctx context.Context, apiKey string, limit int64, window time.Duration) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	key := keyRateLimitKey(apiKey)

	now := time.Now()
	if c.isBlocked(now, key) {
		return false, nil
	}

	current, err := c.incrWithTTL(ctx, key, window)
	if err != nil {
		return false, err
	}

	allowed := current <= limit
	if !allowed {
		c.blockUntilWindowReset(ctx, now, key, window)
	}

	return allowed, nil
}

// CheckKeyDomainRateLimit returns true if the API key + domain request should be allowed.
func (c *Cache) CheckKeyDomainRateLimit(ctx context.Context, apiKey, domain string, limit int64, window time.Duration) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	key := keyDomainRateLimitKey(apiKey, domain)

	now := time.Now()
	if c.isBlocked(now, key) {
		return false, nil
	}

	current, err := c.incrWithTTL(ctx, key, window)
	if err != nil {
		return false, err
	}

	allowed := current <= limit
	if !allowed {
		c.blockUntilWindowReset(ctx, now, key, window)
	}

	return allowed, nil
}

// blockCache 用于缓存“已经超限”的 Redis key，直到窗口 TTL 结束再放行。
// 这样在单 key 被疯狂并发打爆时，能够将大量超限请求从“每次都打 Redis”降到“本地快速拒绝”。
type blockCache struct {
	m             sync.Map // string -> int64(UnixNano)
	size          atomic.Int64
	maxEntries    int64
	sweepInterval time.Duration
	stopCh        chan struct{}
	doneCh        chan struct{}
}

func newBlockCache(maxEntries int64, sweepInterval time.Duration) *blockCache {
	if sweepInterval <= 0 {
		sweepInterval = 30 * time.Second
	}

	bc := &blockCache{
		maxEntries:    maxEntries,
		sweepInterval: sweepInterval,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	go bc.sweeper()
	return bc
}

func (bc *blockCache) Close() {
	if bc == nil {
		return
	}

	// 关闭 stopCh 可能被重复调用（例如多次 Close）；用 recover 保底。
	defer func() { _ = recover() }()
	close(bc.stopCh)
	<-bc.doneCh
}

func (bc *blockCache) IsBlocked(key string, now time.Time) bool {
	if bc == nil {
		return false
	}

	v, ok := bc.m.Load(key)
	if !ok {
		return false
	}

	exp, ok := v.(int64)
	if !ok {
		if _, loaded := bc.m.LoadAndDelete(key); loaded {
			bc.size.Add(-1)
		}
		return false
	}

	if exp > now.UnixNano() {
		return true
	}

	if _, loaded := bc.m.LoadAndDelete(key); loaded {
		bc.size.Add(-1)
	}
	return false
}

func (bc *blockCache) Block(key string, until time.Time) {
	if bc == nil {
		return
	}

	exp := until.UnixNano()
	if exp <= time.Now().UnixNano() {
		return
	}

	if bc.maxEntries > 0 && bc.size.Load() >= bc.maxEntries {
		// 超限保护：仅允许刷新已存在 key 的过期时间，避免被海量随机 key 打爆内存。
		if _, ok := bc.m.Load(key); !ok {
			return
		}
	}

	if _, loaded := bc.m.LoadOrStore(key, exp); !loaded {
		bc.size.Add(1)
	} else {
		// 更新过期时间
		bc.m.Store(key, exp)
	}
}

func (bc *blockCache) sweeper() {
	defer close(bc.doneCh)

	ticker := time.NewTicker(bc.sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bc.sweep(time.Now().UnixNano())
		case <-bc.stopCh:
			bc.sweep(time.Now().UnixNano())
			return
		}
	}
}

func (bc *blockCache) sweep(nowUnixNano int64) {
	if bc == nil {
		return
	}

	bc.m.Range(func(k, v any) bool {
		exp, ok := v.(int64)
		if !ok || exp <= nowUnixNano {
			if _, loaded := bc.m.LoadAndDelete(k); loaded {
				bc.size.Add(-1)
			}
		}
		return true
	})
}
