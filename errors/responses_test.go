package errors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteError(t *testing.T) {
	tests := []struct {
		name           string
		err            *HapaxError
		expectedCode   int
		expectedType   ErrorType
		expectedFields []string
	}{
		{
			name: "hapax error",
			err: &HapaxError{
				Type:      AuthError,
				Message:   "unauthorized",
				Code:      http.StatusUnauthorized,
				RequestID: "test-id",
			},
			expectedCode: http.StatusUnauthorized,
			expectedType: AuthError,
			expectedFields: []string{"type", "message", "request_id"},
		},
		{
			name: "error with details",
			err: &HapaxError{
				Type:      ValidationError,
				Message:   "validation failed",
				Code:      http.StatusBadRequest,
				RequestID: "test-id",
				Details: map[string]interface{}{
					"field": "username",
					"error": "required",
				},
			},
			expectedCode: http.StatusBadRequest,
			expectedType: ValidationError,
			expectedFields: []string{"type", "message", "request_id", "details"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()

			WriteError(rr, tt.err)

			if rr.Code != tt.expectedCode {
				t.Errorf("WriteError() status = %v, want %v", rr.Code, tt.expectedCode)
			}

			contentType := rr.Header().Get("Content-Type")
			if contentType != "application/json" {
				t.Errorf("WriteError() content-type = %v, want application/json", contentType)
			}

			var response map[string]interface{}
			if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
				t.Fatalf("Failed to decode response body: %v", err)
			}

			if errorType, ok := response["type"].(string); !ok || ErrorType(errorType) != tt.expectedType {
				t.Errorf("WriteError() error type = %v, want %v", errorType, tt.expectedType)
			}

			for _, field := range tt.expectedFields {
				if _, exists := response[field]; !exists {
					t.Errorf("WriteError() missing expected field: %s", field)
				}
			}
		})
	}
}
