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
			Model:            "gpt-4",
			MaxContextTokens: 100,
		},
	}
	err := Initialize(cfg)
	assert.NoError(t, err)

	tests := []struct {
		name            string
		contentType     string
		body            interface{}
		expectedStatus  int
		expectedError   bool
		expectedDetails map[string]string // Map of field names to expected error messages
		expectedCode    string            // Expected error code
		suggestion      string            // Expected suggestion message
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
			name:        "missing required content field",
			contentType: "application/json",
			body: CompletionRequest{
				Messages: []Message{
					{Role: "user", Content: ""}, // Empty content
				},
			},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedError:  true,
			expectedDetails: map[string]string{
				"messages[0].content": "field 'content' is required",
			},
			expectedCode: "required_validation_failed",
			suggestion:   "The request format is correct but the content is invalid",
		},
		{
			name:        "invalid role value",
			contentType: "application/json",
			body: CompletionRequest{
				Messages: []Message{
					{Role: "invalid", Content: "Hello"},
				},
			},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedError:  true,
			expectedDetails: map[string]string{
				"messages[0].role": "role must be one of: user, assistant, system",
			},
			expectedCode: "oneof_validation_failed",
			suggestion:   "The request format is correct but the content is invalid",
		},
		{
			name:        "invalid role value",
			contentType: "application/json",
			body: CompletionRequest{
				Messages: []Message{
					{Role: "invalid", Content: "Hello"},
				},
			},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedError:  true,
			expectedDetails: map[string]string{
				"messages[0].role": "role must be one of: user, assistant, system",
			},
			expectedCode: "oneof_validation_failed",
			suggestion:   "The request format is correct but the content is invalid",
		},
		{
			name:        "token limit exceeded",
			contentType: "application/json",
			body: CompletionRequest{
				Messages: []Message{
					{Role: "user", Content: string(make([]byte, 1000))}, // Large content
				},
			},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedError:  true,
			expectedDetails: map[string]string{
				"messages": "token limit exceeded",
			},
			expectedCode: "token_limit_exceeded",
			suggestion:   "The request format is correct but the content is invalid",
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

			// Create request with a test request ID
			req := httptest.NewRequest(http.MethodPost, "/v1/completions", bytes.NewBuffer(bodyBytes))
			req.Header.Set("X-Request-ID", "test-request-id")
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

			// Assert response status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedError {
				var errorResp APIError
				err := json.Unmarshal(w.Body.Bytes(), &errorResp)
				assert.NoError(t, err, "Failed to unmarshal error response")

				// Verify error structure
				assert.Equal(t, "validation_error", errorResp.Type)
				assert.Equal(t, "test-request-id", errorResp.RequestID)
				assert.Equal(t, tt.expectedStatus, errorResp.Code)

				if tt.suggestion != "" {
					assert.Equal(t, tt.suggestion, errorResp.Suggestion)
				}

				// Verify error details
				if tt.expectedDetails != nil {
					assert.Len(t, errorResp.Details, len(tt.expectedDetails))

					// Create a map of field to error message from the response
					actualDetails := make(map[string]string)
					for _, detail := range errorResp.Details {
						actualDetails[detail.Field] = detail.Message
					}

					// Compare expected and actual details
					for field, expectedMsg := range tt.expectedDetails {
						actualMsg, exists := actualDetails[field]
						assert.True(t, exists, "Expected error for field %s not found", field)
						assert.Equal(t, expectedMsg, actualMsg,
							"Error message mismatch for field %s", field)
					}
				}

				// Verify error code if specified
				if tt.expectedCode != "" {
					hasExpectedCode := false
					for _, detail := range errorResp.Details {
						if detail.Code == tt.expectedCode {
							hasExpectedCode = true
							break
						}
					}
					assert.True(t, hasExpectedCode,
						"Expected error code %s not found", tt.expectedCode)
				}
			}
		})
	}
}
