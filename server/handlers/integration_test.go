package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/errors"
	"github.com/teilomillet/hapax/server/metrics"
	"github.com/teilomillet/hapax/server/middleware"
	"github.com/teilomillet/hapax/server/processing"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/server/mocks"
	"go.uber.org/zap"
)

// TestCompletionHandlerIntegration tests the CompletionHandler integrated with:
// - Router for request routing
// - Middleware for request ID and rate limiting
// - Error handling middleware
// - Logging middleware
func TestCompletionHandlerIntegration(t *testing.T) {
	// Create metrics
	m := metrics.NewMetrics()

	// Create mock LLM
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		// If context has timeout header, simulate timeout
		if ctx.Value(middleware.XTestTimeoutKey) != nil {
			// Sleep longer than the timeout
			time.Sleep(5 * time.Second)
		}
		return "Mock response", nil
	})

	// Create logger
	logger := zap.NewNop()

	// Create processor
	cfg := &config.ProcessingConfig{
		RequestTemplates: map[string]string{
			"default":  "{{.Input}}",
			"chat":     "{{range .Messages}}{{.Role}}: {{.Content}}\n{{end}}",
			"function": "Function: {{.FunctionDescription}}\nInput: {{.Input}}",
		},
	}
	processor, err := processing.NewProcessor(cfg, mockLLM)
	require.NoError(t, err)

	// Create handler
	handler := NewCompletionHandler(processor, logger)

	// Create middleware chain
	chain := middleware.RequestID(
		middleware.PrometheusMetrics(m)(
			middleware.RateLimit(m)(
				middleware.Timeout(5*time.Second)(handler),
			),
		),
	)

	// Create test server
	ts := httptest.NewServer(chain)
	defer ts.Close()

	tests := []struct {
		name          string
		method        string
		path          string
		requestBody   interface{}
		headers       map[string]string
		expectedCode  int
		expectedError *errors.ErrorResponse
		setup         func(t *testing.T, ts *httptest.Server)
	}{
		{
			name:         "method not allowed",
			method:       http.MethodGet,
			path:         "/v1/completions",
			expectedCode: http.StatusMethodNotAllowed,
			expectedError: &errors.ErrorResponse{
				Type:    errors.ValidationError,
				Message: "Method not allowed",
				Details: map[string]interface{}{
					"method":          http.MethodGet,
					"allowed_methods": []string{http.MethodPost},
				},
			},
		},
		{
			name:         "missing content type",
			method:       http.MethodPost,
			path:         "/v1/completions",
			requestBody:  CompletionRequest{Input: "test"},
			expectedCode: http.StatusBadRequest,
			expectedError: &errors.ErrorResponse{
				Type:    errors.ValidationError,
				Message: "Content-Type header required",
				Details: map[string]interface{}{
					"required_content_type": "application/json",
				},
			},
		},
		{
			name:         "rate limit exceeded",
			method:       http.MethodPost,
			path:         "/v1/completions",
			headers:      map[string]string{"Content-Type": "application/json"},
			requestBody:  CompletionRequest{Input: "test"},
			expectedCode: http.StatusTooManyRequests,
			expectedError: &errors.ErrorResponse{
				Type:    errors.RateLimitError,
				Message: "Rate limit exceeded",
				Details: map[string]interface{}{
					"limit":  10,
					"window": "1m0s",
				},
			},
			setup: func(t *testing.T, ts *httptest.Server) {
				// Reset rate limiters before starting
				middleware.ResetRateLimiters()

				// Make 10 successful requests first
				for i := 0; i < 10; i++ {
					body, err := json.Marshal(CompletionRequest{Input: "test"})
					require.NoError(t, err)
					req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/completions", bytes.NewReader(body))
					require.NoError(t, err)
					req.Header.Set("Content-Type", "application/json")
					resp, err := http.DefaultClient.Do(req)
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, resp.StatusCode)
					resp.Body.Close()
				}

				// The next request should fail
				body, err := json.Marshal(CompletionRequest{Input: "test"})
				require.NoError(t, err)
				req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/completions", bytes.NewReader(body))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
				resp.Body.Close()
			},
		},
		{
			name:         "malformed json",
			method:       http.MethodPost,
			path:         "/v1/completions",
			headers:      map[string]string{"Content-Type": "application/json"},
			requestBody:  "{invalid json}",
			expectedCode: http.StatusBadRequest,
			expectedError: &errors.ErrorResponse{
				Type:    errors.ValidationError,
				Message: "Invalid completion request format",
				Details: map[string]interface{}{
					"type": "default",
				},
			},
		},
		{
			name:   "context timeout",
			method: http.MethodPost,
			path:   "/v1/completions",
			headers: map[string]string{
				"Content-Type":   "application/json",
				"X-Test-Timeout": "true",
			},
			requestBody:  CompletionRequest{Input: "test"},
			expectedCode: http.StatusGatewayTimeout,
			expectedError: &errors.ErrorResponse{
				Type:    errors.InternalError,
				Message: "Request timeout",
				Details: map[string]interface{}{
					"timeout": "5s",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset rate limiters before each test
			middleware.ResetRateLimiters()

			// Run setup first if it exists
			if tt.setup != nil {
				tt.setup(t, ts)
			}

			// Create request
			var body []byte
			if str, ok := tt.requestBody.(string); ok {
				body = []byte(str)
			} else {
				var err error
				body, err = json.Marshal(tt.requestBody)
				require.NoError(t, err)
			}

			// Create request with context
			req, err := http.NewRequest(tt.method, ts.URL+tt.path, bytes.NewReader(body))
			require.NoError(t, err)

			// Add headers
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			// Send request
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			// Verify status code
			assert.Equal(t, tt.expectedCode, resp.StatusCode)

			if tt.expectedError != nil {
				var gotError errors.ErrorResponse
				err := json.NewDecoder(resp.Body).Decode(&gotError)
				require.NoError(t, err)

				assert.Equal(t, tt.expectedError.Type, gotError.Type)
				assert.Equal(t, tt.expectedError.Message, gotError.Message)
				assert.NotEmpty(t, gotError.RequestID)

				// Compare details, handling slice type differences
				if tt.expectedError.Details != nil {
					assert.Equal(t, len(tt.expectedError.Details), len(gotError.Details))
					for k, v := range tt.expectedError.Details {
						gotV, ok := gotError.Details[k]
						assert.True(t, ok, "missing key %s in error details", k)

						// Special handling for slices
						if expSlice, ok := v.([]string); ok {
							if gotSlice, ok := gotV.([]interface{}); ok {
								assert.Equal(t, len(expSlice), len(gotSlice), "slice length mismatch for key %s", k)
								for i := range expSlice {
									assert.Equal(t, expSlice[i], gotSlice[i].(string))
								}
								continue
							}
						}

						// Special handling for numbers from JSON
						if expInt, ok := v.(int); ok {
							if gotFloat, ok := gotV.(float64); ok {
								assert.Equal(t, float64(expInt), gotFloat)
								continue
							}
						}

						// Regular comparison for other values
						assert.Equal(t, v, gotV)
					}
				}
			}
		})
	}
}
