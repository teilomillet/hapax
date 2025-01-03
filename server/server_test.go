package server

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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

// MockConfigWatcher provides a thread-safe implementation of the config.Watcher interface for testing.
// It simulates a configuration watcher that can handle dynamic configuration updates
// while ensuring safe concurrent access from multiple goroutines.
//
// Thread Safety:
// - Uses RWMutex to allow multiple concurrent readers but exclusive writers
// - Ensures atomic updates of configuration state
// - Safely handles channel operations for config updates
type MockConfigWatcher struct {
	// config holds the current configuration state
	// Protected by mu for thread-safe access
	config *config.Config

	// ch is used to broadcast configuration updates to subscribers
	// This channel is created once during initialization and closed on shutdown
	ch chan *config.Config

	// mu protects concurrent access to the config field
	// Using RWMutex allows multiple readers with exclusive writer access
	mu sync.RWMutex
}

// NewMockConfigWatcher creates a new mock config watcher with the provided initial configuration.
// It initializes the update channel and sets up the initial state.
//
// Parameters:
//   - cfg: The initial configuration to use
//
// Returns:
//   - A new MockConfigWatcher instance ready for use in tests
func NewMockConfigWatcher(cfg *config.Config) *MockConfigWatcher {
	return &MockConfigWatcher{
		config: cfg,
		ch:     make(chan *config.Config),
	}
}

// GetCurrentConfig returns the current configuration in a thread-safe manner.
// Uses a read lock to allow concurrent reads while preventing reads during updates.
//
// Returns:
//   - The current configuration state
func (w *MockConfigWatcher) GetCurrentConfig() *config.Config {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.config
}

// Subscribe returns a channel for receiving configuration updates.
// Subscribers can use this channel to be notified of configuration changes.
// The channel is closed when the watcher is closed.
//
// Returns:
//   - A read-only channel that receives configuration updates
func (w *MockConfigWatcher) Subscribe() <-chan *config.Config {
	return w.ch
}

// UpdateConfig safely updates the current configuration and notifies all subscribers.
// This method is thread-safe and ensures atomic updates of the configuration.
//
// Parameters:
//   - cfg: The new configuration to apply
//
// Implementation Notes:
//   - Acquires a write lock to prevent concurrent access during update
//   - Updates the configuration atomically
//   - Notifies all subscribers through the update channel
func (w *MockConfigWatcher) UpdateConfig(cfg *config.Config) {
	w.mu.Lock()
	w.config = cfg
	w.mu.Unlock()
	w.ch <- cfg
}

// Close implements proper cleanup of the watcher resources.
// It closes the update channel to signal subscribers that no more updates will be sent.
//
// Returns:
//   - error: Always returns nil as the operation cannot fail
func (w *MockConfigWatcher) Close() error {
	close(w.ch)
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
	// Helper function to check if port is in use
	portInUse := func(port int) bool {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			return true
		}
		ln.Close()
		return false
	}

	// Helper function to wait for port to be available
	waitForPortAvailable := func(port int) error {
		for i := 0; i < 50; i++ { // Try for 5 seconds (50 * 100ms)
			if !portInUse(port) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("port %d is still in use after timeout", port)
	}

	// Wait for ports to be available
	ports := []int{9081, 9082}
	for _, port := range ports {
		require.NoError(t, waitForPortAvailable(port), "Port %d is still in use", port)
	}

	// Create test configuration
	logger := zaptest.NewLogger(t)

	// Create a mock LLM
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "test response", nil
	})

	// Create initial configuration
	initialConfig := &config.Config{
		Server: config.ServerConfig{
			Port:            9081,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			MaxHeaderBytes:  1 << 20,
			ShutdownTimeout: 5 * time.Second,
		},
		LLM: config.LLMConfig{
			Provider: "mock",
			Model:    "test",
			Options: map[string]interface{}{
				"temperature": 0.7,
			},
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Routes: []config.RouteConfig{
			{
				Path:    "/health",
				Handler: "health",
				Version: "v1",
			},
		},
	}

	// Create and start server
	watcher := NewMockConfigWatcher(initialConfig)
	server, err := NewServerWithConfig(watcher, mockLLM, logger)
	require.NoError(t, err)

	// Create a context with timeout for the entire test
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start server in goroutine
	serverErrChan := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			serverErrChan <- err
		}
		close(serverErrChan)
	}()

	// Wait for server to be ready
	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/health", initialConfig.Server.Port))
		if err != nil {
			t.Logf("Connection attempt failed: %v", err)
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 100*time.Millisecond, "Server failed to start")

	// Run tests
	t.Run("Health Check", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/health", initialConfig.Server.Port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("Configuration Update", func(t *testing.T) {
		// Create new configuration with different port
		newConfig := &config.Config{
			Server: config.ServerConfig{
				Port:            9082,
				ReadTimeout:     30 * time.Second,
				WriteTimeout:    30 * time.Second,
				MaxHeaderBytes:  1 << 20,
				ShutdownTimeout: 5 * time.Second,
			},
			LLM: config.LLMConfig{
				Provider: "mock",
				Model:    "test",
				Options: map[string]interface{}{
					"temperature": 0.7,
				},
			},
			Logging: config.LoggingConfig{
				Level:  "info",
				Format: "json",
			},
			Routes: []config.RouteConfig{
				{
					Path:    "/health",
					Handler: "health",
					Version: "v1",
				},
			},
		}

		// Update configuration
		watcher.UpdateConfig(newConfig)

		// Wait for server to be ready on new port
		require.Eventually(t, func() bool {
			resp, err := http.Get(fmt.Sprintf("http://localhost:%d/health", newConfig.Server.Port))
			if err != nil {
				t.Logf("Connection attempt failed: %v", err)
				return false
			}
			defer resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}, 5*time.Second, 100*time.Millisecond, "Server failed to start on new port")

		// Check that old port is no longer in use
		require.NoError(t, waitForPortAvailable(initialConfig.Server.Port), "Old port %d is still in use", initialConfig.Server.Port)
	})

	// Cleanup
	cancel()

	// Wait for server to shut down
	select {
	case err := <-serverErrChan:
		if err != nil && err != context.Canceled {
			require.NoError(t, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Server failed to shut down")
	}

	// Wait for ports to be released
	for _, port := range ports {
		require.NoError(t, waitForPortAvailable(port), "Port %d was not released after server shutdown", port)
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
