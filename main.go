package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/teilomillet/gollm"
)

// ServerConfig holds server configuration values
type ServerConfig struct {
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	MaxHeaderBytes  int
	ShutdownTimeout time.Duration
}

// DefaultConfig returns a ServerConfig with sensible defaults
func DefaultConfig() ServerConfig {
	return ServerConfig{
		Port:            8080,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    30 * time.Second,
		MaxHeaderBytes:  1 << 20, // 1MB
		ShutdownTimeout: 30 * time.Second,
	}
}

// CompletionHandler handles LLM completion requests
type CompletionHandler struct {
	llm gollm.LLM
}

// NewCompletionHandler creates a new CompletionHandler
func NewCompletionHandler(llm gollm.LLM) CompletionHandler {
	return CompletionHandler{llm: llm}
}

func (h CompletionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Prompt string `json:"prompt"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	prompt := gollm.NewPrompt(req.Prompt)
	resp, err := h.llm.Generate(r.Context(), prompt)
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"completion": resp,
	})
}

// HealthHandler handles health check requests
type HealthHandler struct{}

func (h HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Router handles HTTP routing
type Router struct {
	completionHandler CompletionHandler
	healthHandler     HealthHandler
}

// NewRouter creates a new Router
func NewRouter(completionHandler CompletionHandler) Router {
	return Router{
		completionHandler: completionHandler,
		healthHandler:     HealthHandler{},
	}
}

func (r Router) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", r.healthHandler)
	mux.Handle("/v1/completions", r.completionHandler)
	return mux
}

// Server represents the LLM gateway server
type Server struct {
	httpServer *http.Server
	config     ServerConfig
}

// NewServer creates a new Server
func NewServer(config ServerConfig, router Router) Server {
	return Server{
		config: config,
		httpServer: &http.Server{
			Addr:           fmt.Sprintf(":%d", config.Port),
			Handler:        router.Handler(),
			ReadTimeout:    config.ReadTimeout,
			WriteTimeout:   config.WriteTimeout,
			MaxHeaderBytes: config.MaxHeaderBytes,
		},
	}
}

// Start starts the server and blocks until shutdown
func (s Server) Start(ctx context.Context) error {
	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		log.Printf("Server started on port %d", s.config.Port)
		errCh <- s.httpServer.ListenAndServe()
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		// Create shutdown context with timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
		defer cancel()

		// Attempt graceful shutdown
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown error: %w", err)
		}
		return nil
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	}
}

func main() {
	// Initialize LLM provider
	llm, err := gollm.NewLLM()
	if err != nil {
		log.Fatalf("Failed to initialize LLM: %v", err)
	}

	// Create handlers and router
	completionHandler := NewCompletionHandler(llm)
	router := NewRouter(completionHandler)

	// Create and start server
	config := DefaultConfig()
	server := NewServer(config, router)

	if err := server.Start(context.Background()); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
