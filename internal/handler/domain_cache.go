package handler

import (
	"context"
	"strings"
	"time"

	"mailapi/internal/model"
	"mailapi/internal/store"
)

// 域名列表基本不变（来源：config.yaml 启动时同步到 Mongo），高并发下每次都查 Mongo 会非常浪费。
// 用小 TTL 的本地缓存把热路径上的 Mongo 调用降到“每个进程每 TTL 一次”。
const domainsCacheTTL = 30 * time.Second

type domainCacheEntry struct {
	domains    []model.Domain
	domainSet  map[string]struct{}
	expiresAt  time.Time
	lastUpdate time.Time
}

func (h *Handler) getDomainCache(ctx context.Context) (*domainCacheEntry, error) {
	now := time.Now()
	if e := h.domainCache.Load(); e != nil && now.Before(e.expiresAt) {
		return e, nil
	}

	h.domainCacheMu.Lock()
	defer h.domainCacheMu.Unlock()

	if e := h.domainCache.Load(); e != nil && now.Before(e.expiresAt) {
		return e, nil
	}

	domains, err := h.store.ListDomains(ctx)
	if err != nil {
		// Mongo 短暂不可用时，尽量返回已有缓存以保持 API 可用性。
		if e := h.domainCache.Load(); e != nil {
			return e, nil
		}
		return nil, err
	}

	set := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		// 域名比较应当大小写不敏感
		set[strings.ToLower(d.Domain)] = struct{}{}
	}

	e := &domainCacheEntry{
		domains:    domains,
		domainSet:  set,
		expiresAt:  now.Add(domainsCacheTTL),
		lastUpdate: now,
	}
	h.domainCache.Store(e)
	return e, nil
}

func (h *Handler) domainAvailable(ctx context.Context, domain string) (bool, error) {
	e, err := h.getDomainCache(ctx)
	if err != nil {
		return false, err
	}
	_, ok := e.domainSet[strings.ToLower(domain)]
	return ok, nil
}

func (h *Handler) domainAvailableOrError(ctx context.Context, domain string) error {
	ok, err := h.domainAvailable(ctx, domain)
	if err != nil {
		return err
	}
	if !ok {
		return store.ErrNotFound
	}
	return nil
}
