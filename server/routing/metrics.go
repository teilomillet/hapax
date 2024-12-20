package routing

import (
	"net/http"

	"github.com/teilomillet/hapax/server/metrics"
)

// RegisterMetricsRoutes adds routes for Prometheus metrics
func RegisterMetricsRoutes(mux *http.ServeMux, m *metrics.Metrics) {
	mux.Handle("/metrics", m.Handler())
}
