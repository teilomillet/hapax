package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/teilomillet/hapax/server/metrics"
	"github.com/teilomillet/hapax/server/middleware"
)

func TestRateLimitMetrics(t *testing.T) {
	// Create new metrics instance for testing
	m := metrics.NewMetrics()

	// Reset rate limiters
	middleware.ResetRateLimiters()

	// Create test handler
	handler := middleware.RateLimit(m)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Create test server
	server := httptest.NewServer(handler)
	defer server.Close()

	// Make requests to trigger rate limit
	client := &http.Client{}
	testIP := "127.0.0.1"

	// Make 11 requests (1 more than limit)
	for i := 0; i < 11; i++ {
		req, err := http.NewRequest("GET", server.URL, nil)
		assert.NoError(t, err)
		req.RemoteAddr = testIP + ":1234" // Set test IP

		resp, err := client.Do(req)
		assert.NoError(t, err)
		resp.Body.Close()

		// Last request should be rate limited
		if i == 10 {
			assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)

			// Check rate limit metric
			rateLimitCount := testutil.ToFloat64(m.RateLimitHits.WithLabelValues(testIP))
			assert.Equal(t, float64(1), rateLimitCount)
		}
	}
}
