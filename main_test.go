package main

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
			wantErrContain: "LLM error",
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
	handler := router.Handler()

	// Test completion endpoint
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		t.Error("completion endpoint not found")
	}

	// Test health endpoint
	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		t.Error("health endpoint not found")
	}

	// Test unknown endpoint
	req = httptest.NewRequest(http.MethodGet, "/unknown", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Error("unknown endpoint should return 404")
	}
}

// TestServer tests server lifecycle
func TestServer(t *testing.T) {
	config := ServerConfig{
		Port:            8081,
		ReadTimeout:     1 * time.Second,
		WriteTimeout:    1 * time.Second,
		MaxHeaderBytes:  1 << 20,
		ShutdownTimeout: 1 * time.Second,
	}

	llm := &MockLLM{}
	completionHandler := NewCompletionHandler(llm)
	router := NewRouter(completionHandler)
	server := NewServer(config, router)

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

// TestDefaultConfig tests default configuration
func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Port != 8080 {
		t.Errorf("unexpected default port: got %v want %v", config.Port, 8080)
	}

	if config.ReadTimeout != 30*time.Second {
		t.Errorf("unexpected default read timeout: got %v want %v",
			config.ReadTimeout, 30*time.Second)
	}

	if config.WriteTimeout != 30*time.Second {
		t.Errorf("unexpected default write timeout: got %v want %v",
			config.WriteTimeout, 30*time.Second)
	}

	if config.MaxHeaderBytes != 1<<20 {
		t.Errorf("unexpected default max header bytes: got %v want %v",
			config.MaxHeaderBytes, 1<<20)
	}

	if config.ShutdownTimeout != 30*time.Second {
		t.Errorf("unexpected default shutdown timeout: got %v want %v",
			config.ShutdownTimeout, 30*time.Second)
	}
}
