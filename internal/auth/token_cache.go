package auth

import (
	"sync"
	"sync/atomic"
	"time"
)

type tokenCacheEntry struct {
	claims   Claims
	expires  time.Time
	storedAt time.Time
}

type tokenCache struct {
	m             sync.Map // string -> *tokenCacheEntry
	size          atomic.Int64
	maxEntries    int64
	sweepInterval time.Duration
	lastSweepUnix atomic.Int64
}

func newTokenCache(maxEntries int64, sweepInterval time.Duration) *tokenCache {
	if maxEntries <= 0 {
		maxEntries = 1
	}
	if sweepInterval <= 0 {
		sweepInterval = 30 * time.Second
	}
	return &tokenCache{
		maxEntries:    maxEntries,
		sweepInterval: sweepInterval,
	}
}

func (tc *tokenCache) Get(token string, now time.Time) (*Claims, bool) {
	if tc == nil {
		return nil, false
	}

	v, ok := tc.m.Load(token)
	if !ok {
		tc.maybeSweep(now)
		return nil, false
	}
	e, ok := v.(*tokenCacheEntry)
	if !ok || e == nil {
		if _, loaded := tc.m.LoadAndDelete(token); loaded {
			tc.size.Add(-1)
		}
		tc.maybeSweep(now)
		return nil, false
	}

	if !e.expires.IsZero() && !now.Before(e.expires) {
		if _, loaded := tc.m.LoadAndDelete(token); loaded {
			tc.size.Add(-1)
		}
		tc.maybeSweep(now)
		return nil, false
	}

	// 返回副本，避免调用方意外修改缓存对象。
	c := e.claims
	tc.maybeSweep(now)
	return &c, true
}

func (tc *tokenCache) Put(token string, claims *Claims) {
	if tc == nil || claims == nil || token == "" {
		return
	}

	// 过期时间用于快速淘汰；没有 ExpiresAt 时不缓存（避免永久占用）。
	var expires time.Time
	if claims.ExpiresAt != nil {
		expires = claims.ExpiresAt.Time
	}
	if expires.IsZero() {
		return
	}

	now := time.Now()
	if !now.Before(expires) {
		return
	}

	if tc.maxEntries > 0 && tc.size.Load() >= tc.maxEntries {
		// 容量保护：不引入新 key（避免被海量 token 打爆内存）；允许刷新已存在 key。
		if _, ok := tc.m.Load(token); !ok {
			tc.maybeSweep(now)
			return
		}
	}

	e := &tokenCacheEntry{
		claims:   *claims,
		expires:  expires,
		storedAt: now,
	}

	if _, loaded := tc.m.LoadOrStore(token, e); !loaded {
		tc.size.Add(1)
	} else {
		tc.m.Store(token, e)
	}

	tc.maybeSweep(now)
}

func (tc *tokenCache) maybeSweep(now time.Time) {
	if tc == nil {
		return
	}

	nowUnix := now.UnixNano()
	last := tc.lastSweepUnix.Load()
	if last != 0 && time.Duration(nowUnix-last) < tc.sweepInterval {
		return
	}
	if !tc.lastSweepUnix.CompareAndSwap(last, nowUnix) {
		return
	}

	tc.m.Range(func(k, v any) bool {
		e, ok := v.(*tokenCacheEntry)
		if !ok || e == nil || (!e.expires.IsZero() && !now.Before(e.expires)) {
			if _, loaded := tc.m.LoadAndDelete(k); loaded {
				tc.size.Add(-1)
			}
		}
		return true
	})
}

