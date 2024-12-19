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
	handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	tests := []struct {
		name           string
		method         string
		expectedStatus int
		checkBody      bool
	}{
		{
			name:           "normal request",
			method:         "GET",
			expectedStatus: http.StatusOK,
			checkBody:      true,
		},
		{
			name:           "preflight request",
			method:         "OPTIONS",
			expectedStatus: http.StatusOK,
			checkBody:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)
			assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
			assert.NotEmpty(t, rec.Header().Get("Access-Control-Allow-Methods"))
			assert.NotEmpty(t, rec.Header().Get("Access-Control-Allow-Headers"))

			if tt.checkBody {
				assert.Equal(t, "ok", rec.Body.String())
			}
		})
	}
}
