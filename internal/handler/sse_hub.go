package handler

import (
	"context"
	"sync"
	"time"

	"mailapi/internal/cache"
	"mailapi/internal/metrics"

	"github.com/redis/go-redis/v9"
)

// sseHub 将 Redis Pub/Sub 的订阅连接做“进程内复用”，并负责 fan-out 给多个 SSE 客户端。
// 目标：避免每个 SSE 连接都建立一个 Redis 订阅连接，导致 Redis 连接数与 goroutine 线性膨胀。
type sseHub struct {
	cache cache.Interface

	mu     sync.Mutex
	topics map[string]*sseTopic
}

type sseTopic struct {
	cache     cache.Interface
	accountID string
	sub       *redis.PubSub
	subMu     sync.Mutex

	stopCh chan struct{}
	doneCh chan struct{}

	mu      sync.Mutex
	clients map[chan string]struct{}
}

func newSSEHub(c cache.Interface) *sseHub {
	return &sseHub{
		cache:  c,
		topics: make(map[string]*sseTopic),
	}
}

func (h *sseHub) Subscribe(accountID string) (<-chan string, func()) {
	h.mu.Lock()

	newTopic := false
	t := h.topics[accountID]
	if t == nil {
		// 用后台 context 建立订阅连接，避免某个 SSE 连接断开导致同 account 的其它连接被连带关闭。
		sub := h.cache.Subscribe(context.Background(), accountID)
		t = &sseTopic{
			cache:     h.cache,
			accountID: accountID,
			sub:       sub,
			stopCh:    make(chan struct{}),
			doneCh:    make(chan struct{}),
			clients:   make(map[chan string]struct{}),
		}
		h.topics[accountID] = t
		newTopic = true
		go t.run()
	}

	clientCh := make(chan string, 16)
	t.mu.Lock()
	t.clients[clientCh] = struct{}{}
	t.mu.Unlock()

	h.mu.Unlock()

	// 指标在 /metrics 被抓取前可能尚未注册，但对未注册指标执行 Inc/Dec 是安全的，
	// 这样可以保证“先有 SSE 连接、后启用 metrics 抓取”时数值仍然准确。
	metrics.SSEClients.Inc()
	if newTopic {
		metrics.SSETopics.Inc()
	}

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.unsubscribe(accountID, clientCh)
		})
	}
	return clientCh, unsubscribe
}

func (h *sseHub) unsubscribe(accountID string, ch chan string) {
	h.mu.Lock()
	t := h.topics[accountID]
	if t == nil {
		h.mu.Unlock()
		safeCloseChan(ch)
		return
	}

	removed, empty := t.removeClient(ch)
	if empty {
		delete(h.topics, accountID)
		h.mu.Unlock()
		if removed {
			metrics.SSEClients.Dec()
		}
		metrics.SSETopics.Dec()
		t.close()
		return
	}

	h.mu.Unlock()
	if removed {
		metrics.SSEClients.Dec()
	}
}

func (t *sseTopic) removeClient(ch chan string) (removed bool, empty bool) {
	t.mu.Lock()
	if _, ok := t.clients[ch]; ok {
		delete(t.clients, ch)
		safeCloseChan(ch)
		removed = true
	}
	empty = len(t.clients) == 0
	t.mu.Unlock()
	return removed, empty
}

func (t *sseTopic) close() {
	// stop run loop first, then close redis subscription.
	close(t.stopCh)
	t.subMu.Lock()
	_ = t.sub.Close()
	t.subMu.Unlock()
	<-t.doneCh

	t.mu.Lock()
	for ch := range t.clients {
		delete(t.clients, ch)
		safeCloseChan(ch)
	}
	t.mu.Unlock()
}

func (t *sseTopic) run() {
	defer close(t.doneCh)

	// Redis 订阅可能因为网络抖动/重连导致 channel 关闭；这里做自动重订阅，避免 SSE 静默“卡死”。
	// 退避上限 30s，避免 Redis 故障时 busy loop 占用 CPU。
	backoff := time.Second

	for {
		// Channel 默认 buffer 太小会导致慢消费时阻塞，适当加大以减少抖动。
		t.subMu.Lock()
		sub := t.sub
		t.subMu.Unlock()
		ch := sub.Channel(redis.WithChannelSize(256))

		for {
			select {
			case <-t.stopCh:
				return
			case msg, ok := <-ch:
				if !ok {
					goto resubscribe
				}
				// 一旦能正常收到消息，说明订阅健康，重置退避。
				backoff = time.Second
				t.broadcast(msg.Payload)
			}
		}

	resubscribe:
		select {
		case <-t.stopCh:
			return
		default:
		}

		// 关闭旧订阅并创建新订阅（用后台 ctx，避免被单个 SSE 请求 ctx 取消连带关闭）。
		t.subMu.Lock()
		_ = t.sub.Close()
		t.sub = t.cache.Subscribe(context.Background(), t.accountID)
		t.subMu.Unlock()
		metrics.SSEResubscribeTotal.Inc()

		timer := time.NewTimer(backoff)
		select {
		case <-t.stopCh:
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (t *sseTopic) broadcast(payload string) {
	t.mu.Lock()
	for ch := range t.clients {
		select {
		case ch <- payload:
		default:
			// 慢客户端：直接断开，避免无限堆积占用内存。
			delete(t.clients, ch)
			metrics.SSEDroppedClientsTotal.Inc()
			metrics.SSEClients.Dec()
			safeCloseChan(ch)
		}
	}
	t.mu.Unlock()
}

func safeCloseChan(ch chan string) {
	defer func() { _ = recover() }()
	close(ch)
}
