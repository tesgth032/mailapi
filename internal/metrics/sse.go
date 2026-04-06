package metrics

import "github.com/prometheus/client_golang/prometheus"

// 注意：这些指标默认不启用，只有当 debug server 开启 /metrics 时才会被注册并采集。

var (
	// SSEClients 统计当前进程内 SSE 长连接数量（API 进程级别）。
	SSEClients = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "mailapi",
		Subsystem: "sse",
		Name:      "clients",
		Help:      "Number of SSE clients currently connected to this API process.",
	})

	// SSETopics 统计当前进程内活跃的 account topic 数量（每个 account 复用 1 个 Redis 订阅）。
	SSETopics = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "mailapi",
		Subsystem: "sse",
		Name:      "topics",
		Help:      "Number of active SSE topics (accounts) in this API process.",
	})

	// SSEDroppedClientsTotal 统计由于“慢客户端”被踢掉的次数。
	SSEDroppedClientsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "mailapi",
		Subsystem: "sse",
		Name:      "dropped_clients_total",
		Help:      "Total number of SSE clients dropped due to slow consumption.",
	})

	// SSEResubscribeTotal 统计 Redis Pub/Sub 自动重订阅次数（网络抖动/断链后恢复）。
	SSEResubscribeTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "mailapi",
		Subsystem: "sse",
		Name:      "resubscribe_total",
		Help:      "Total number of SSE Redis Pub/Sub resubscribe events.",
	})
)
