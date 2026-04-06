package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Interface defines all cache operations. Implemented by *Cache.
type Interface interface {
	// Ping 用于 readiness 检查：验证 Redis 可用性（轻量）。
	Ping(ctx context.Context) error
	SetAddress(ctx context.Context, addr string, ttl time.Duration) error
	HasAddress(ctx context.Context, addr string) (bool, error)
	RemoveAddress(ctx context.Context, addr string) error
	Publish(ctx context.Context, accountID string, payload string) error
	Subscribe(ctx context.Context, accountID string) *redis.PubSub
	CheckRateLimit(ctx context.Context, key string, limit int64, window time.Duration) (bool, error)
	CheckSMTPRateLimit(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error)
	CheckKeyRateLimit(ctx context.Context, apiKey string, limit int64, window time.Duration) (bool, error)
	CheckKeyDomainRateLimit(ctx context.Context, apiKey, domain string, limit int64, window time.Duration) (bool, error)
	Client() *redis.Client
	Close() error
}
