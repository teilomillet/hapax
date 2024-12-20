package routing

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/teilomillet/hapax/server/metrics"
)

func TestRegisterMetricsRoutes(t *testing.T) {
	// Create new metrics instance for testing
	m := metrics.NewMetrics()

	// Create new mux
	mux := http.NewServeMux()
	RegisterMetricsRoutes(mux, m)

	// Create test server
	server := httptest.NewServer(mux)
	defer server.Close()

	// Make a test request to increment some metrics
	m.RequestsTotal.WithLabelValues("/test", "200").Inc()
	m.ErrorsTotal.WithLabelValues("server_error").Inc()
	m.RateLimitHits.WithLabelValues("test_client").Inc()

	// Test metrics endpoint
	resp, err := http.Get(server.URL + "/metrics")
	assert.NoError(t, err)
	defer resp.Body.Close()

	// Check response code
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	assert.Contains(t, contentType, "text/plain")

	// Read response body
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	// Verify response contains our metrics
	bodyStr := string(body)
	expectedMetrics := []string{
		"hapax_http_requests_total",
		"hapax_errors_total",
		"hapax_rate_limit_hits_total",
	}

	for _, metric := range expectedMetrics {
		assert.Contains(t, bodyStr, metric, "response should contain metric '%s'", metric)
	}
}
