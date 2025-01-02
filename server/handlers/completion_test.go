// Package handlers provides HTTP handlers for the Hapax server.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/errors"
	"github.com/teilomillet/hapax/server/middleware"
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
			requestBody: CompletionRequest{
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
			requestBody: CompletionRequest{
				Input:               "What's the weather in Paris?",
				FunctionDescription: "Get weather data for a location",
			},
			mockResponse:   `{"function": "get_weather", "location": "Paris"}`,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "empty request",
			requestType:    "",
			requestBody:    CompletionRequest{},
			expectedStatus: http.StatusBadRequest,
			expectedError: &errors.ErrorResponse{
				Type:      errors.ValidationError,
				Message:   "Either input or messages must be provided",
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
					"type":        "default",
					"max_size":    "512KB",
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
			requestBody: CompletionRequest{
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
			requestBody: CompletionRequest{
				Input:               "What's the weather?",
				FunctionDescription: string(make([]byte, 10*1024)), // 10KB description
			},
			expectedStatus: http.StatusBadRequest,
			expectedError: &errors.ErrorResponse{
				Type:      errors.ValidationError,
				Message:   "Function description too large",
				RequestID: "test-123",
				Details: map[string]interface{}{
					"type":        "function",
					"max_size":    "5KB",
					"actual_size": "10KB",
				},
			},
		},
		{
			name:        "mixed message and input",
			requestType: "chat",
			requestBody: CompletionRequest{
				Messages: []gollm.PromptMessage{
					{Role: "system", Content: "You are a helpful assistant"},
				},
				Input: "Hi there",
			},
			mockResponse:   "Hello! I am here to help.",
			expectedStatus: http.StatusOK,
		},
		{
			name:        "function with messages",
			requestType: "function",
			requestBody: CompletionRequest{
				FunctionDescription: "Get weather data",
				Messages: []gollm.PromptMessage{
					{Role: "user", Content: "What's the weather in Paris?"},
				},
			},
			mockResponse:   `{"function": "get_weather", "location": "Paris"}`,
			expectedStatus: http.StatusOK,
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
			req = req.WithContext(context.WithValue(req.Context(), middleware.RequestIDKey, "test-123"))
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

func TestCurlMultiMessageExample(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create a mock LLM that simulates a conversation
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		// Return appropriate responses based on conversation context
		return "I'd be happy to help you with Python programming!", nil
	})

	// Configure processor with templates
	cfg := &config.ProcessingConfig{
		RequestTemplates: map[string]string{
			"chat": "{{range .Messages}}{{.Role}}: {{.Content}}\n{{end}}",
		},
	}

	processor, err := processing.NewProcessor(cfg, mockLLM)
	require.NoError(t, err)

	// Create the handler with middleware chain
	baseHandler := NewCompletionHandler(processor, logger)
	handler := middleware.RequestID(baseHandler) // Wrap with RequestID middleware

	// This simulates the following curl command:
	/*
		curl -X POST "http://localhost:8081/v1/completions?type=chat" \
		  -H "Content-Type: application/json" \
		  -d '{
		    "messages": [
		      {"role": "system", "content": "You are a helpful programming assistant."},
		      {"role": "user", "content": "I need help with Python."},
		      {"role": "assistant", "content": "I'd be happy to help! What specific Python question do you have?"},
		      {"role": "user", "content": "How do I read a file?"}
		    ]
		  }'
	*/

	// Create the request body
	requestBody := CompletionRequest{
		Messages: []gollm.PromptMessage{
			{Role: "system", Content: "You are a helpful programming assistant."},
			{Role: "user", Content: "I need help with Python."},
			{Role: "assistant", Content: "I'd be happy to help! What specific Python question do you have?"},
			{Role: "user", Content: "How do I read a file?"},
		},
	}

	body, err := json.Marshal(requestBody)
	require.NoError(t, err)

	// Create test request
	req := httptest.NewRequest(http.MethodPost, "/v1/completions?type=chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Record response
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Verify response
	assert.Equal(t, http.StatusOK, w.Code)

	var resp processing.Response
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "I'd be happy to help you with Python programming!", resp.Content)

	// Verify response headers
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	requestID := w.Header().Get("X-Request-ID")
	assert.NotEmpty(t, requestID, "X-Request-ID header should be set")
	assert.Len(t, requestID, 36, "Request ID should be a UUID")
}

func TestCurlCommandFormat(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create mock LLM
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "Hello! How can I help you today?", nil
	})

	// Configure processor
	cfg := &config.ProcessingConfig{
		RequestTemplates: map[string]string{
			"chat": "{{range .Messages}}{{.Role}}: {{.Content}}\n{{end}}",
		},
	}

	processor, err := processing.NewProcessor(cfg, mockLLM)
	require.NoError(t, err)

	// Create handler with middleware
	baseHandler := NewCompletionHandler(processor, logger)
	handler := middleware.RequestID(baseHandler)

	tests := []struct {
		name           string
		requestBody    string
		expectedStatus int
		expectedResp   string
	}{
		{
			name: "simple chat message",
			requestBody: `{
				"messages": [
					{
						"role": "user",
						"content": "Hello"
					}
				]
			}`,
			expectedStatus: http.StatusOK,
			expectedResp:   "Hello! How can I help you today?",
		},
		{
			name: "chat with system message",
			requestBody: `{
				"messages": [
					{
						"role": "system",
						"content": "You are a helpful assistant."
					},
					{
						"role": "user",
						"content": "Hello"
					}
				]
			}`,
			expectedStatus: http.StatusOK,
			expectedResp:   "Hello! How can I help you today?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request
			req := httptest.NewRequest(http.MethodPost, "/v1/completions?type=chat", strings.NewReader(tt.requestBody))
			req.Header.Set("Content-Type", "application/json")

			// Record response
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// Verify status code
			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var resp processing.Response
				err := json.NewDecoder(w.Body).Decode(&resp)
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResp, resp.Content)
			}

			// Verify headers
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			requestID := w.Header().Get("X-Request-ID")
			assert.NotEmpty(t, requestID)
			assert.Len(t, requestID, 36)
		})
	}
}
