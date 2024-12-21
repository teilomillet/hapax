package provider

import "github.com/prometheus/client_golang/prometheus"

// initializeMetrics sets up Prometheus metrics
func (m *Manager) initializeMetrics(registry *prometheus.Registry) {
	m.healthCheckDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "hapax_health_check_duration_seconds",
		Help: "Duration of provider health checks",
	})

	m.healthCheckErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hapax_health_check_errors_total",
		Help: "Number of health check errors by provider",
	}, []string{"provider"})

	m.requestLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hapax_request_latency_seconds",
		Help: "Latency of provider requests",
	}, []string{"provider"})

	m.deduplicatedRequests = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hapax_deduplicated_requests_total",
		Help: "Number of deduplicated requests",
	})

	m.healthyProviders = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hapax_healthy_providers",
		Help: "Number of healthy providers",
	}, []string{"provider"})

	registry.MustRegister(m.healthCheckDuration)
	registry.MustRegister(m.healthCheckErrors)
	registry.MustRegister(m.requestLatency)
	registry.MustRegister(m.deduplicatedRequests)
	registry.MustRegister(m.healthyProviders)
}
