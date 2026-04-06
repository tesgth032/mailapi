package metrics

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	metricsEnabled atomic.Bool
	registerOnce   sync.Once
)

// Enable 启用并注册自定义指标。
// 该函数是幂等的；未启用 /metrics 时建议不要调用（避免引入无意义的采集开销）。
func Enable() {
	metricsEnabled.Store(true)
	registerOnce.Do(func() {
		prometheus.MustRegister(
			SSEClients,
			SSETopics,
			SSEDroppedClientsTotal,
			SSEResubscribeTotal,
		)
	})
}

// IsEnabled 返回当前进程是否启用了 metrics（即 debug server 是否暴露 /metrics）。
func IsEnabled() bool {
	return metricsEnabled.Load()
}

// Handler 返回 Prometheus metrics handler（使用默认 registry）。
// 注意：该 handler 不做鉴权，建议仅在本地 debug server 上暴露。
func Handler() http.Handler {
	Enable()
	return promhttp.Handler()
}
