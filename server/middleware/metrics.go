package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/teilomillet/hapax/server/metrics"
)

// PrometheusMetrics middleware records HTTP metrics using Prometheus.
func PrometheusMetrics(m *metrics.Metrics) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Track active requests
			m.ActiveRequests.WithLabelValues(r.URL.Path).Inc()
			defer m.ActiveRequests.WithLabelValues(r.URL.Path).Dec()

			// Create response writer that captures status code
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Call next handler
			next.ServeHTTP(rw, r)

			// Record metrics
			duration := time.Since(start).Seconds()
			status := strconv.Itoa(rw.statusCode)

			m.RequestsTotal.WithLabelValues(r.URL.Path, status).Inc()
			m.RequestDuration.WithLabelValues(r.URL.Path).Observe(duration)

			// Record errors
			if rw.statusCode >= 500 {
				m.ErrorsTotal.WithLabelValues("server_error").Inc()
			} else if rw.statusCode >= 400 {
				m.ErrorsTotal.WithLabelValues("client_error").Inc()
			}
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode    int
	wroteHeader   bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.statusCode = code
		rw.ResponseWriter.WriteHeader(code)
		rw.wroteHeader = true
	}
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}
