package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRequestID(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler should see the X-Request-ID header
		assert.NotEmpty(t, r.Header.Get("X-Request-ID"))
	}))

	tests := []struct {
		name           string
		providedReqID  string
		shouldBeReused bool
	}{
		{
			name:           "generates new request ID",
			providedReqID:  "",
			shouldBeReused: false,
		},
		{
			name:           "reuses provided request ID",
			providedReqID:  "test-id-123",
			shouldBeReused: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.providedReqID != "" {
				req.Header.Set("X-Request-ID", tt.providedReqID)
			}
			
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			respID := rec.Header().Get("X-Request-ID")
			assert.NotEmpty(t, respID)
			
			if tt.shouldBeReused {
				assert.Equal(t, tt.providedReqID, respID)
			} else {
				assert.NotEqual(t, tt.providedReqID, respID)
			}
		})
	}
}

func TestRequestTimer(t *testing.T) {
	handler := RequestTimer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	handler := PanicRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	tests := []struct {
		name           string
		apiKey         string
		expectedStatus int
	}{
		{
			name:           "valid API key",
			apiKey:         "test-key",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "missing API key",
			apiKey:         "",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := Authentication(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/", nil)
			if tt.apiKey != "" {
				req.Header.Set("X-API-Key", tt.apiKey)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)
		})
	}
}

func TestRateLimit(t *testing.T) {
	handler := RateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test normal request flow
	t.Run("allows requests within limit", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	// Test rate limiting
	t.Run("blocks excessive requests", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345" // Use specific IP for this test

		// Make many requests in quick succession
		var lastStatus int
		for i := 0; i < 30; i++ {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			lastStatus = rec.Code
			if lastStatus == http.StatusTooManyRequests {
				break
			}
		}

		assert.Equal(t, http.StatusTooManyRequests, lastStatus)
	})

	// Test rate limit reset
	t.Run("allows requests after cooldown", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.2:12345" // Use different IP

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		time.Sleep(time.Second) // Wait for rate limit to reset

		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}
