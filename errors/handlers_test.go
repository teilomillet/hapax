package errors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestErrorHandler(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name           string
		handler       http.Handler
		expectedCode  int
		expectPanic   bool
	}{
		{
			name: "normal handler",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
			expectedCode: http.StatusOK,
			expectPanic:  false,
		},
		{
			name: "panicking handler",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("test panic")
			}),
			expectedCode: http.StatusInternalServerError,
			expectPanic:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test request
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("X-Request-ID", "test-request-id")
			
			// Create a response recorder
			rr := httptest.NewRecorder()

			// Wrap the handler with our error handler
			handler := ErrorHandler(logger)(tt.handler)

			// Execute the handler
			handler.ServeHTTP(rr, req)

			// Check the status code
			if rr.Code != tt.expectedCode {
				t.Errorf("handler returned wrong status code: got %v want %v",
					rr.Code, tt.expectedCode)
			}
		})
	}
}

func TestLogError(t *testing.T) {
	logger := zap.NewNop()
	requestID := "test-request-id"

	// Test logging a HapaxError
	hapaxErr := NewValidationError(requestID, "test error", nil)
	LogError(logger, hapaxErr, requestID)

	// Test logging a standard error
	standardErr := NewInternalError(requestID, nil)
	LogError(logger, standardErr, requestID)
	
	// Note: Since we're using a NOP logger, we can't verify the output
	// In a real application, you might want to use zap/zaptest for more detailed assertions
}
