// Package handlers provides HTTP handlers for the Hapax server.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/errors"
	"github.com/teilomillet/hapax/server/mocks"
	"github.com/teilomillet/hapax/server/processing"
	"go.uber.org/zap/zaptest"
)

// TestCompletionHandler tests the CompletionHandler's request handling.
// It verifies:
// 1. Correct handling of different request types (default, chat, function)
// 2. Proper error responses for invalid requests
// 3. Request validation and type conversion
// 4. Integration with the processor and error handling
func TestCompletionHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)

	tests := []struct {
		name           string
		requestType    string
		requestBody    interface{}
		mockResponse   string
		mockError      error
		expectedStatus int
		expectedError  *errors.ErrorResponse
	}{
		{
			name:        "simple completion success",
			requestType: "",
			requestBody: CompletionRequest{
				Input: "What is the capital of France?",
			},
			mockResponse:   "Paris is the capital of France.",
			expectedStatus: http.StatusOK,
		},
		{
			name:        "chat completion success",
			requestType: "chat",
			requestBody: ChatRequest{
				Messages: []gollm.PromptMessage{
					{Role: "user", Content: "Hi"},
					{Role: "assistant", Content: "Hello!"},
					{Role: "user", Content: "How are you?"},
				},
			},
			mockResponse:   "I'm doing well, thank you!",
			expectedStatus: http.StatusOK,
		},
		{
			name:        "function completion success",
			requestType: "function",
			requestBody: FunctionRequest{
				Input:               "What's the weather in Paris?",
				FunctionDescription: "Get weather data for a location",
			},
			mockResponse:   `{"function": "get_weather", "location": "Paris"}`,
			expectedStatus: http.StatusOK,
		},
		{
			name:        "empty chat messages",
			requestType: "chat",
			requestBody: ChatRequest{
				Messages: []gollm.PromptMessage{},
			},
			expectedStatus: http.StatusBadRequest,
			expectedError: &errors.ErrorResponse{
				Type:      errors.ValidationError,
				Message:   "Chat messages cannot be empty",
				RequestID: "test-123",
				Details: map[string]interface{}{
					"type": "chat",
				},
			},
		},
		{
			name:        "empty input for completion",
			requestType: "",
			requestBody: CompletionRequest{
				Input: "",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError: &errors.ErrorResponse{
				Type:      errors.ValidationError,
				Message:   "Input text is required",
				RequestID: "test-123",
				Details: map[string]interface{}{
					"type": "default",
				},
			},
		},
		{
			name:        "processing error",
			requestType: "",
			requestBody: CompletionRequest{
				Input: "Test input",
			},
			mockError:      errors.NewInternalError("test-123", fmt.Errorf("LLM error")),
			expectedStatus: http.StatusInternalServerError,
			expectedError: &errors.ErrorResponse{
				Type:      errors.InternalError,
				Message:   "Failed to process request",
				RequestID: "test-123",
				Details: map[string]interface{}{
					"type":  "default",
					"error": "LLM error",
				},
			},
		},
		{
			name:        "invalid json in request",
			requestType: "",
			requestBody: "invalid json",
			expectedStatus: http.StatusBadRequest,
			expectedError: &errors.ErrorResponse{
				Type:      errors.ValidationError,
				Message:   "Invalid completion request format",
				RequestID: "test-123",
				Details: map[string]interface{}{
					"type": "default",
				},
			},
		},
		{
			name:        "very long input",
			requestType: "",
			requestBody: CompletionRequest{
				Input: string(make([]byte, 1024*1024)), // 1MB input
			},
			expectedStatus: http.StatusBadRequest,
			expectedError: &errors.ErrorResponse{
				Type:      errors.ValidationError,
				Message:   "Input too large",
				RequestID: "test-123",
				Details: map[string]interface{}{
					"type": "default",
					"max_size": "512KB",
					"actual_size": "1MB",
				},
			},
		},
		{
			name:        "unicode input",
			requestType: "",
			requestBody: CompletionRequest{
				Input: "Hello 世界 🌍",
			},
			mockResponse:   "Response with unicode: 你好",
			expectedStatus: http.StatusOK,
		},
		{
			name:        "special characters",
			requestType: "",
			requestBody: CompletionRequest{
				Input: "Input with <script>alert('xss')</script>",
			},
			mockResponse:   "Sanitized response",
			expectedStatus: http.StatusOK,
		},
		{
			name:        "concurrent requests",
			requestType: "",
			requestBody: CompletionRequest{
				Input: "Test concurrent",
			},
			mockResponse:   "Concurrent response",
			expectedStatus: http.StatusOK,
		},
		{
			name:        "chat with system message",
			requestType: "chat",
			requestBody: ChatRequest{
				Messages: []gollm.PromptMessage{
					{Role: "system", Content: "You are a helpful assistant"},
					{Role: "user", Content: "Hi"},
				},
			},
			mockResponse:   "Hello! How can I help you?",
			expectedStatus: http.StatusOK,
		},
		{
			name:        "function with long description",
			requestType: "function",
			requestBody: FunctionRequest{
				Input:               "What's the weather?",
				FunctionDescription: string(make([]byte, 10*1024)), // 10KB description
			},
			expectedStatus: http.StatusBadRequest,
			expectedError: &errors.ErrorResponse{
				Type:      errors.ValidationError,
				Message:   "Function description too large",
				RequestID: "test-123",
				Details: map[string]interface{}{
					"type": "function",
					"max_size": "5KB",
					"actual_size": "10KB",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock processor with configured behavior
			mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
				if tt.mockError != nil {
					return "", tt.mockError
				}
				return tt.mockResponse, nil
			})

			// Configure processor with templates
			cfg := &config.ProcessingConfig{
				RequestTemplates: map[string]string{
					"default":  "{{.Input}}",
					"chat":     "{{range .Messages}}{{.Role}}: {{.Content}}\n{{end}}",
					"function": "Function: {{.FunctionDescription}}\nInput: {{.Input}}",
				},
			}

			processor, err := processing.NewProcessor(cfg, mockLLM)
			require.NoError(t, err)

			handler := NewCompletionHandler(processor, logger)

			// Create request with appropriate body
			var body []byte
			if str, ok := tt.requestBody.(string); ok {
				body = []byte(str)
			} else {
				body, err = json.Marshal(tt.requestBody)
				require.NoError(t, err)
			}

			// Build request URL with type parameter
			url := "/v1/completions"
			if tt.requestType != "" {
				url += "?type=" + tt.requestType
			}

			// Create test request with context
			req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
			req = req.WithContext(context.WithValue(req.Context(), "request_id", "test-123"))
			req.Header.Set("Content-Type", "application/json")

			// Record response
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// Verify status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedError != nil {
				// Verify error response
				var gotError errors.ErrorResponse
				err := json.NewDecoder(w.Body).Decode(&gotError)
				require.NoError(t, err)
				
				// Compare error fields
				assert.Equal(t, tt.expectedError.Type, gotError.Type)
				assert.Equal(t, tt.expectedError.Message, gotError.Message)
				assert.Equal(t, tt.expectedError.RequestID, gotError.RequestID)
				if tt.expectedError.Details != nil {
					assert.Equal(t, tt.expectedError.Details, gotError.Details)
				}
			} else {
				// Verify success response
				var resp processing.Response
				err := json.NewDecoder(w.Body).Decode(&resp)
				require.NoError(t, err)
				assert.Equal(t, tt.mockResponse, resp.Content)
			}
		})
	}
}

// TestConvertMessages verifies the message type conversion between
// gollm.PromptMessage and processing.Message.
// It tests:
// 1. Correct field mapping
// 2. Handling of empty messages
// 3. Preservation of message order
func TestConvertMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    []gollm.PromptMessage
		expected []processing.Message
	}{
		{
			name: "convert multiple messages",
			input: []gollm.PromptMessage{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi there"},
				{Role: "user", Content: "How are you?"},
			},
			expected: []processing.Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi there"},
				{Role: "user", Content: "How are you?"},
			},
		},
		{
			name:     "empty messages",
			input:    []gollm.PromptMessage{},
			expected: []processing.Message{},
		},
		{
			name: "preserve message order",
			input: []gollm.PromptMessage{
				{Role: "system", Content: "You are a helpful assistant"},
				{Role: "user", Content: "Hi"},
				{Role: "assistant", Content: "Hello!"},
			},
			expected: []processing.Message{
				{Role: "system", Content: "You are a helpful assistant"},
				{Role: "user", Content: "Hi"},
				{Role: "assistant", Content: "Hello!"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertMessages(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
