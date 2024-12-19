package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
)

// TestCompletionHandler tests the completion handler
func TestCompletionHandler(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		body          string
		generateFunc  func(context.Context, *gollm.Prompt) (string, error)
		wantStatus    int
		wantResponse  string
		wantErrContain string
	}{
		{
			name:   "success",
			method: http.MethodPost,
			body:   `{"prompt": "Hello"}`,
			generateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
				return "Hello, world!", nil
			},
			wantStatus:  http.StatusOK,
			wantResponse: `{"completion":"Hello, world!"}` + "\n",
		},
		{
			name:       "invalid method",
			method:     http.MethodGet,
			wantStatus: http.StatusMethodNotAllowed,
			wantErrContain: "Method not allowed",
		},
		{
			name:           "invalid json",
			method:         http.MethodPost,
			body:           `invalid json`,
			wantStatus:     http.StatusBadRequest,
			wantErrContain: "Invalid request body",
		},
		{
			name:           "missing prompt",
			method:         http.MethodPost,
			body:           `{}`,
			wantStatus:     http.StatusBadRequest,
			wantErrContain: "prompt is required",
		},
		{
			name:   "llm error",
			method: http.MethodPost,
			body:   `{"prompt": "Hello"}`,
			generateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
				return "", errors.New("llm error")
			},
			wantStatus:     http.StatusInternalServerError,
			wantErrContain: "Internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock LLM
			llm := &MockLLM{GenerateFunc: tt.generateFunc}
			handler := NewCompletionHandler(llm)

			// Create request
			var body io.Reader
			if tt.body != "" {
				body = bytes.NewBufferString(tt.body)
			}
			req := httptest.NewRequest(tt.method, "/v1/completions", body)
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
			if tt.wantErrContain != "" && !strings.Contains(w.Body.String(), tt.wantErrContain) {
				t.Errorf("handler returned unexpected error: got %v want %v",
					w.Body.String(), tt.wantErrContain)
			}
		})
	}
}

// TestRouter tests the router
func TestRouter(t *testing.T) {
	llm := &MockLLM{}
	completionHandler := NewCompletionHandler(llm)
	router := NewRouter(completionHandler)

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

// TestServer tests server lifecycle
func TestServer(t *testing.T) {
	cfg := config.ServerConfig{
		Port:            8081,
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    10 * time.Second,
		MaxHeaderBytes:  1 << 20,
		ShutdownTimeout: 30 * time.Second,
	}

	llm := &MockLLM{}
	completionHandler := NewCompletionHandler(llm)
	router := NewRouter(completionHandler)
	server := NewServer(cfg, router)

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

	// Send request to verify server is running
	resp, err := http.Get("http://localhost:8081/health")
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Health check failed: got %v", resp.StatusCode)
	}

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
