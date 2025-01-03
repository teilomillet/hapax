package server

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/errors"
	"github.com/teilomillet/hapax/server/handlers"
	"github.com/teilomillet/hapax/server/middleware"
	"github.com/teilomillet/hapax/server/mocks"
	"github.com/teilomillet/hapax/server/processing"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

// MockConfigWatcher implements a test version of the configuration watcher
type MockConfigWatcher struct {
	currentConfig atomic.Value
}

func NewMockConfigWatcher(cfg *config.Config) *MockConfigWatcher {
	mcw := &MockConfigWatcher{}
	mcw.currentConfig.Store(cfg)
	return mcw
}

func (m *MockConfigWatcher) GetCurrentConfig() *config.Config {
	return m.currentConfig.Load().(*config.Config)
}

func (m *MockConfigWatcher) Subscribe() <-chan *config.Config {
	return make(chan *config.Config)
}

func (m *MockConfigWatcher) Close() error {
	return nil
}

// TestCompletionHandler tests the completion handler for various scenarios.
// It includes tests for invalid methods, invalid JSON input, missing prompts, LLM errors, and successful completions.
// Each test checks the response status and body to ensure the handler behaves as expected.
func TestCompletionHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)

	tests := []struct {
		name           string
		method         string
		body           interface{}
		generateFunc   func(context.Context, *gollm.Prompt) (string, error)
		wantStatus     int
		wantResponse   string
		wantErrContain string
		expectJSON     bool
	}{
		{
			name:           "invalid method",
			method:         http.MethodGet,
			wantStatus:     http.StatusMethodNotAllowed,
			wantErrContain: "Method not allowed",
			expectJSON:     true,
		},
		{
			name:           "invalid json",
			method:         http.MethodPost,
			body:           "invalid json",
			wantStatus:     http.StatusBadRequest,
			wantErrContain: "Invalid completion request format",
			expectJSON:     true,
		},
		{
			name:           "missing prompt",
			method:         http.MethodPost,
			body:           map[string]string{},
			wantStatus:     http.StatusBadRequest,
			wantErrContain: "Either input or messages must be provided",
			expectJSON:     true,
		},
		{
			name:   "llm error",
			method: http.MethodPost,
			body:   map[string]string{"input": "Hello"},
			generateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
				return "", stderrors.New("llm error")
			},
			wantStatus:     http.StatusInternalServerError,
			wantErrContain: "Failed to process request",
			expectJSON:     true,
		},
		{
			name:   "success",
			method: http.MethodPost,
			body:   map[string]string{"input": "Hello"},
			generateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
				return "Hello, world!", nil
			},
			wantStatus:   http.StatusOK,
			wantResponse: `{"content":"Hello, world!"}` + "\n",
			expectJSON:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock LLM
			mockLLM := mocks.NewMockLLM(tt.generateFunc)

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

			// Create handler using the handlers package
			handler := handlers.NewCompletionHandler(processor, logger)

			// Create request
			var body io.Reader
			if tt.body != nil {
				if str, ok := tt.body.(string); ok {
					body = bytes.NewBufferString(str)
				} else {
					bodyBytes, _ := json.Marshal(tt.body)
					body = bytes.NewBuffer(bodyBytes)
				}
			}
			req := httptest.NewRequest(tt.method, "/v1/completions", body)
			req = req.WithContext(context.WithValue(req.Context(), middleware.RequestIDKey, "test-123"))
			if tt.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()

			// Handle request
			handler.ServeHTTP(w, req)

			// Check status code
			if w.Code != tt.wantStatus {
				t.Errorf("handler returned wrong status code: got %v want %v",
					w.Code, tt.wantStatus)
			}

			// Check response
			if tt.wantResponse != "" && w.Body.String() != tt.wantResponse {
				t.Errorf("handler returned unexpected body: got %v want %v",
					w.Body.String(), tt.wantResponse)
			}

			// Check error message
			if tt.wantErrContain != "" {
				var errorResp errors.ErrorResponse
				if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
					t.Fatalf("Failed to decode error response: %v", err)
				}
				if !strings.Contains(errorResp.Message, tt.wantErrContain) {
					t.Errorf("handler returned unexpected error: got %v want %v",
						errorResp.Message, tt.wantErrContain)
				}
			}

			if tt.expectJSON {
				contentType := w.Header().Get("Content-Type")
				if contentType != "application/json" {
					t.Errorf("handler returned wrong content type: got %v want application/json",
						contentType)
				}
			}
		})
	}
}

// TestRouter tests the router's endpoints and their expected responses.
// It verifies the behavior of the completion endpoint, health check, and handling of invalid routes.
// Each test checks the response status to ensure the router is correctly configured.
func TestRouter(t *testing.T) {
	logger := zaptest.NewLogger(t)
	mockLLM := mocks.NewMockLLM(nil)
	cfg := config.DefaultConfig()
	router := NewRouter(mockLLM, cfg, logger)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "completion endpoint",
			path:       "/v1/completions",
			wantStatus: http.StatusMethodNotAllowed, // GET not allowed
		},
		{
			name:       "health endpoint",
			path:       "/health",
			wantStatus: http.StatusOK,
		},
		{
			name:       "not found",
			path:       "/invalid",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("handler returned wrong status code: got %v want %v",
					w.Code, tt.wantStatus)
			}
		})
	}
}

// TestRouterWithMiddleware tests the router with middleware
func TestRouterWithMiddleware(t *testing.T) {
	logger := zaptest.NewLogger(t)
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "test response", nil
	})
	cfg := config.DefaultConfig()
	router := NewRouter(mockLLM, cfg, logger)

	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
		checkHeaders   bool
	}{
		{
			name:           "health check",
			method:         "GET",
			path:           "/health",
			expectedStatus: http.StatusOK,
			checkHeaders:   true,
		},
		{
			name:           "completion endpoint",
			method:         "POST",
			path:           "/v1/completions",
			expectedStatus: http.StatusOK,
			checkHeaders:   true,
		},
		{
			name:           "not found",
			method:         "GET",
			path:           "/nonexistent",
			expectedStatus: http.StatusNotFound,
			checkHeaders:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.method == "POST" {
				bodyBytes, _ := json.Marshal(map[string]string{"input": "test"})
				body = bytes.NewBuffer(bodyBytes)
			}

			req := httptest.NewRequest(tt.method, tt.path, body)
			if tt.method == "POST" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)

			if tt.checkHeaders {
				// Check middleware headers
				assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
				assert.NotEmpty(t, rec.Header().Get("X-Response-Time"))
				assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
			}
		})
	}
}

// TestServer tests the server lifecycle, including starting and stopping the server.
// It ensures that the server can handle configuration updates without service interruption.
// This includes verifying that the server shuts down gracefully and starts correctly with new settings.
func TestServer(t *testing.T) {
	// Create a complete configuration for testing
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:            8081,
			ReadTimeout:     10 * time.Second,
			WriteTimeout:    10 * time.Second,
			MaxHeaderBytes:  1 << 20,
			ShutdownTimeout: 30 * time.Second,
			HTTP3: &config.HTTP3Config{
				Enabled:                    false,
				Port:                       8443,
				MaxStreamReceiveWindow:     10 * 1024 * 1024,
				MaxConnectionReceiveWindow: 15 * 1024 * 1024,
				MaxBiStreamsConcurrent:     100,
				MaxUniStreamsConcurrent:    100,
				Enable0RTT:                 false,
				Allow0RTTReplay:            false,
				Max0RTTSize:                1024 * 1024,
				UDPReceiveBufferSize:       1024 * 1024,
				IdleTimeout:                30 * time.Second,
			},
		},
		LLM: config.LLMConfig{
			Provider: "mock",
			Model:    "test",
		},
	}

	// Create test logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// Create mock LLM with a test response
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "test response", nil
	})

	// Create mock config watcher
	mockWatcher := mocks.NewMockConfigWatcher(cfg)

	// Create server with mocked dependencies
	server, err := NewServerWithConfig(mockWatcher, mockLLM, logger)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Create context with cancel for server lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	t.Run("Health Check", func(t *testing.T) {
		resp, err := http.Get("http://localhost:8081/health")
		if err != nil {
			t.Fatalf("Failed to connect to server: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Health check failed: got %v, want %v",
				resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("Configuration Update", func(t *testing.T) {
		// Create new configuration with different port
		newCfg := &config.Config{
			Server: config.ServerConfig{
				Port:            8082,
				ReadTimeout:     10 * time.Second,
				WriteTimeout:    10 * time.Second,
				MaxHeaderBytes:  1 << 20,
				ShutdownTimeout: 30 * time.Second,
				HTTP3: &config.HTTP3Config{
					Enabled:                    false,
					Port:                       8444,
					MaxStreamReceiveWindow:     10 * 1024 * 1024,
					MaxConnectionReceiveWindow: 15 * 1024 * 1024,
					MaxBiStreamsConcurrent:     100,
					MaxUniStreamsConcurrent:    100,
					Enable0RTT:                 false,
					Allow0RTTReplay:            false,
					Max0RTTSize:                1024 * 1024,
					UDPReceiveBufferSize:       1024 * 1024,
					IdleTimeout:                30 * time.Second,
				},
			},
			LLM: config.LLMConfig{
				Provider: "mock",
				Model:    "test",
			},
		}

		// Update configuration
		if err := server.updateServerConfig(newCfg); err != nil {
			t.Fatalf("Failed to update server config: %v", err)
		}

		// The waitForServer method now ensures the server is ready before we test
		resp, err := http.Get("http://localhost:8082/health")
		if err != nil {
			t.Fatalf("Failed to connect to server on new port: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Health check on new port failed: got %v, want %v",
				resp.StatusCode, http.StatusOK)
		}
	})

	// Trigger server shutdown
	cancel()

	// Check server stopped without error
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Server error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Server did not shut down within timeout")
	}
}

// DefaultConfig returns the default server configuration
func DefaultConfig() config.ServerConfig {
	return config.ServerConfig{
		Port:            8080,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    30 * time.Second,
		MaxHeaderBytes:  1 << 20,
		ShutdownTimeout: 30 * time.Second,
	}
}

// TestDefaultConfig tests the default server configuration
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Port != 8080 {
		t.Errorf("unexpected default port: got %v want %v", cfg.Port, 8080)
	}

	if cfg.ReadTimeout != 30*time.Second {
		t.Errorf("unexpected default read timeout: got %v want %v",
			cfg.ReadTimeout, 30*time.Second)
	}

	if cfg.WriteTimeout != 30*time.Second {
		t.Errorf("unexpected default write timeout: got %v want %v",
			cfg.WriteTimeout, 30*time.Second)
	}

	if cfg.MaxHeaderBytes != 1<<20 {
		t.Errorf("unexpected default max header bytes: got %v want %v",
			cfg.MaxHeaderBytes, 1<<20)
	}

	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("unexpected default shutdown timeout: got %v want %v",
			cfg.ShutdownTimeout, 30*time.Second)
	}
}

func TestConfigWatcher(t *testing.T) {
	// Create temporary config file for testing
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpfile.Name())

	// Write initial configuration
	initialConfig := `
server:
    port: 8081
llm:
    provider: "mock"
    model: "test"
`
	if _, err := tmpfile.Write([]byte(initialConfig)); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	tmpfile.Close()

	// Create logger for testing
	logger, _ := zap.NewDevelopment()

	// Create config watcher
	watcher, err := config.NewConfigWatcher(tmpfile.Name(), logger)
	if err != nil {
		t.Fatalf("Failed to create config watcher: %v", err)
	}
	defer watcher.Close()

	// Subscribe to configuration updates
	updates := watcher.Subscribe()

	// Verify initial configuration
	cfg := watcher.GetCurrentConfig()
	if cfg.Server.Port != 8081 {
		t.Errorf("Unexpected initial port: got %v, want %v",
			cfg.Server.Port, 8081)
	}

	// Write new configuration
	newConfig := `
server:
    port: 8082
llm:
    provider: "mock"
    model: "test"
`
	if err := os.WriteFile(tmpfile.Name(), []byte(newConfig), 0644); err != nil {
		t.Fatalf("Failed to write new config: %v", err)
	}

	// Wait for configuration update
	select {
	case updatedConfig := <-updates:
		if updatedConfig.Server.Port != 8082 {
			t.Errorf("Unexpected updated port: got %v, want %v",
				updatedConfig.Server.Port, 8082)
		}
	case <-time.After(time.Second):
		t.Error("Timeout waiting for config update")
	}
}
