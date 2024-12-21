package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/teilomillet/hapax/server/metrics"
)

// PrometheusMetrics middleware records HTTP metrics using Prometheus.
// It wraps the HTTP handler to measure request duration and active requests.
// It takes a Metrics object as an argument to track metrics.
func PrometheusMetrics(m *metrics.Metrics) func(next http.Handler) http.Handler {
    // Return a function that takes an http.Handler and returns another http.Handler
    return func(next http.Handler) http.Handler {
        // Return an http.HandlerFunc that wraps the original handler
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Record the start time of the request
            start := time.Now()

            // Track active requests
            // Increment the active request count for the current URL path
            m.ActiveRequests.WithLabelValues(r.URL.Path).Inc()
            // Decrement the active request count when the request is done
            defer m.ActiveRequests.WithLabelValues(r.URL.Path).Dec()

            // Create a response writer that captures the status code
            // This allows us to intercept the status code returned by the handler
            // and record metrics about the response status
            rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

            // Call the next handler in the chain
            next.ServeHTTP(rw, r)

            // Record metrics
            // Calculate the request duration
            duration := time.Since(start).Seconds()
            // Convert the status code to a string
            status := strconv.Itoa(rw.statusCode)

            // Increment the total request count for the current URL path and status code
            m.RequestsTotal.WithLabelValues(r.URL.Path, status).Inc()
            // Record the request duration for the current URL path
            m.RequestDuration.WithLabelValues(r.URL.Path).Observe(duration)

            // Record errors
            // Check if the status code indicates a server error
            if rw.statusCode >= 500 {
                // Increment the error count for server errors
                m.ErrorsTotal.WithLabelValues("server_error").Inc()
            } else if rw.statusCode >= 400 {
                // Increment the error count for client errors
                m.ErrorsTotal.WithLabelValues("client_error").Inc()
            }
        })
    }
}

// responseWriter wraps http.ResponseWriter to capture the status code
// It holds the status code and a flag to check if the header has been written.
type responseWriter struct {
    http.ResponseWriter
    statusCode    int
    wroteHeader   bool
}

// WriteHeader captures the status code and writes it to the response.
// It overrides the default behavior of the ResponseWriter.
func (rw *responseWriter) WriteHeader(code int) {
    // Store the status code
    rw.statusCode = code
    // Mark that the header has been written
    rw.wroteHeader = true
    // Call the original WriteHeader method
    rw.ResponseWriter.WriteHeader(code)
}

// Write captures the response body and allows us to record metrics.
// It overrides the default behavior of the ResponseWriter.
func (rw *responseWriter) Write(b []byte) (int, error) {
    // If the header has not been written, write it with a status code of 200
    if !rw.wroteHeader {
        rw.WriteHeader(http.StatusOK)
    }
    // Call the original Write method to write the response
    return rw.ResponseWriter.Write(b)
}
