package validation

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/teilomillet/hapax/config"
)

func TestValidateCompletion(t *testing.T) {
	// Initialize middleware with config
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Model: "gpt-4",
			MaxContextTokens: 100,
		},
	}
	err := Initialize(cfg)
	assert.NoError(t, err)

	tests := []struct {
		name           string
		contentType    string
		body           interface{}
		expectedStatus int
		expectedError  bool
	}{
		{
			name:        "valid request",
			contentType: "application/json",
			body: CompletionRequest{
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			expectedStatus: http.StatusOK,
			expectedError:  false,
		},
		{
			name:           "missing content type",
			contentType:    "",
			body:          map[string]string{},
			expectedStatus: http.StatusBadRequest,
			expectedError:  true,
		},
		{
			name:           "wrong content type",
			contentType:    "text/plain",
			body:          map[string]string{},
			expectedStatus: http.StatusBadRequest,
			expectedError:  true,
		},
		{
			name:        "invalid json",
			contentType: "application/json",
			body:       "invalid json",
			expectedStatus: http.StatusBadRequest,
			expectedError:  true,
		},
		{
			name:        "validation error - missing required field",
			contentType: "application/json",
			body: CompletionRequest{
				Messages: []Message{
					{Role: "user"}, // missing Content
				},
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  true,
		},
		{
			name:        "validation error - invalid role",
			contentType: "application/json",
			body: CompletionRequest{
				Messages: []Message{
					{Role: "invalid", Content: "Hello"},
				},
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request body
			var bodyBytes []byte
			var err error
			
			switch v := tt.body.(type) {
			case string:
				bodyBytes = []byte(v)
			default:
				bodyBytes, err = json.Marshal(tt.body)
				assert.NoError(t, err)
			}

			// Create request
			req := httptest.NewRequest(http.MethodPost, "/v1/completions", bytes.NewBuffer(bodyBytes))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Create test handler
			handler := ValidateCompletion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			// Handle request
			handler.ServeHTTP(w, req)

			// Assert response
			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedError {
				var errorResp map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &errorResp)
				assert.NoError(t, err)
				assert.Equal(t, "validation_error", errorResp["type"])
			}
		})
	}
}
