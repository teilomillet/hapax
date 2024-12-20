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

func TestPrometheusMetrics(t *testing.T) {
	// Create new metrics instance for testing
	m := metrics.NewMetrics()

	tests := []struct {
		name           string
		handler       http.HandlerFunc
		expectedCode  int
		expectedPath  string
		expectedStatus string
	}{
		{
			name: "success request",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			expectedCode:   http.StatusOK,
			expectedPath:   "/",
			expectedStatus: "200",
		},
		{
			name: "error request",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectedCode:   http.StatusInternalServerError,
			expectedPath:   "/",
			expectedStatus: "500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			handler := middleware.PrometheusMetrics(m)(tt.handler)
			server := httptest.NewServer(handler)
			defer server.Close()

			// Make request
			resp, err := http.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			// Check response code
			assert.Equal(t, tt.expectedCode, resp.StatusCode)

			// Check request metrics
			requestCount := testutil.ToFloat64(m.RequestsTotal.WithLabelValues(tt.expectedPath, tt.expectedStatus))
			assert.Equal(t, float64(1), requestCount)

			// Check active requests (should be 0 after request completes)
			activeRequests := testutil.ToFloat64(m.ActiveRequests)
			assert.Equal(t, float64(0), activeRequests)

			// Check error metrics for 5xx responses
			if tt.expectedCode >= 500 {
				errorCount := testutil.ToFloat64(m.ErrorsTotal.WithLabelValues("server_error"))
				assert.Equal(t, float64(1), errorCount)
			}
		})
	}
}
