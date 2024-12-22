package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/teilomillet/hapax/server/metrics"
	"github.com/teilomillet/hapax/server/middleware"
)

func TestRequestID(t *testing.T) {
	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler should see request ID in context
		requestID := r.Context().Value(middleware.RequestIDKey).(string)
		assert.NotEmpty(t, requestID)
		assert.Equal(t, requestID, w.Header().Get("X-Request-ID"))
	}))

	tests := []struct {
		name string
	}{
		{
			name: "generates new request ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			rec := httptest.NewRecorder()
			
			handler.ServeHTTP(rec, req)

			// Check response header
			respID := rec.Header().Get("X-Request-ID")
			assert.NotEmpty(t, respID)

			// Request ID should be a UUID
			assert.Len(t, respID, 36) // UUID v4 length
		})
	}
}

func TestRequestTimer(t *testing.T) {
	handler := middleware.RequestTimer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond) // Simulate some work
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	respTime := rec.Header().Get("X-Response-Time")
	assert.NotEmpty(t, respTime)
	
	duration, err := time.ParseDuration(respTime)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, duration, 10*time.Millisecond)
}

func TestPanicRecovery(t *testing.T) {
	handler := middleware.PanicRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestCORS(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		expectedStatus int
		expectedHeaders map[string]string
	}{
		{
			name:   "preflight request",
			method: "OPTIONS",
			expectedStatus: http.StatusNoContent,
			expectedHeaders: map[string]string{
				"Access-Control-Allow-Origin":  "*",
				"Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
				"Access-Control-Allow-Headers": "Accept, Authorization, Content-Type, X-CSRF-Token",
			},
		},
		{
			name:   "normal request",
			method: "GET",
			expectedStatus: http.StatusOK,
			expectedHeaders: map[string]string{
				"Access-Control-Allow-Origin":  "*",
				"Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
				"Access-Control-Allow-Headers": "Accept, Authorization, Content-Type, X-CSRF-Token",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := middleware.CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(tt.method, "/", nil)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)
			for key, value := range tt.expectedHeaders {
				assert.Equal(t, value, rr.Header().Get(key))
			}
		})
	}
}

func TestAuthentication(t *testing.T) {
	// Create test handler
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.Authentication(nextHandler)

	// Test without auth header
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test with invalid auth header
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "invalid")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test with valid auth header
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTimeout(t *testing.T) {
	// Create test handler that sleeps
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.Timeout(1 * time.Second)(nextHandler)

	// Test timeout
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusGatewayTimeout, w.Code)

	// Test success (no timeout)
	nextHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler = middleware.Timeout(1 * time.Second)(nextHandler)
	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimit(t *testing.T) {
	// Reset metrics registry
	prometheus.DefaultRegisterer = prometheus.NewRegistry()

	// Create metrics
	m := metrics.NewMetrics()

	// Create test handler
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Create middleware handler
	handler := middleware.RateLimit(m)(nextHandler)

	// Test rate limit
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if i < 10 {
			assert.Equal(t, http.StatusOK, w.Code)
		} else {
			assert.Equal(t, http.StatusTooManyRequests, w.Code)
		}
	}
}
