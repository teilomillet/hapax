package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics encapsulates Prometheus metrics for the server.
type Metrics struct {
	registry        *prometheus.Registry
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	ActiveRequests  *prometheus.GaugeVec
	ErrorsTotal     *prometheus.CounterVec
	RateLimitHits   *prometheus.CounterVec
}

// NewMetrics creates a new Metrics instance with a custom registry.
func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	factory := promauto.With(registry)

	m := &Metrics{
		registry: registry,
		RequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "hapax_http_requests_total",
				Help: "Total number of HTTP requests by endpoint and status",
			},
			[]string{"endpoint", "status"},
		),
		RequestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "hapax_http_request_duration_seconds",
				Help:    "Duration of HTTP requests in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"endpoint"},
		),
		ActiveRequests: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "hapax_http_active_requests",
				Help: "Number of currently active HTTP requests",
			},
			[]string{"endpoint"},
		),
		ErrorsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "hapax_errors_total",
				Help: "Total number of errors by type",
			},
			[]string{"type"},
		),
		RateLimitHits: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "hapax_rate_limit_hits_total",
				Help: "Total number of rate limit hits by client",
			},
			[]string{"client"},
		),
	}

	// Register default Go metrics
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	// Initialize some default metrics
	m.RequestsTotal.WithLabelValues("/health", "200").Add(0)
	m.RequestsTotal.WithLabelValues("/metrics", "200").Add(0)
	m.RequestDuration.WithLabelValues("/health").Observe(0)
	m.RequestDuration.WithLabelValues("/metrics").Observe(0)
	m.ActiveRequests.WithLabelValues("queued").Add(0)
	m.ActiveRequests.WithLabelValues("processing").Add(0)

	return m
}

// Handler returns a handler for the metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: false, // Disable OpenMetrics format to avoid escaping=values
	})
}
