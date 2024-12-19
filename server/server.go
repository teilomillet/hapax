package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
)

// CompletionRequest represents an incoming completion request
type CompletionRequest struct {
	Prompt string `json:"prompt"`
}

// CompletionResponse represents the response to a completion request
type CompletionResponse struct {
	Completion string `json:"completion"`
}

// CompletionHandler handles completion requests
type CompletionHandler struct {
	llm gollm.LLM
}

// NewCompletionHandler creates a new completion handler
func NewCompletionHandler(llm gollm.LLM) *CompletionHandler {
	return &CompletionHandler{llm: llm}
}

// ServeHTTP implements http.Handler
func (h *CompletionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CompletionRequest
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
		log.Printf("Generation error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CompletionResponse{
		Completion: resp,
	})
}

// Router handles HTTP routing
type Router struct {
	completion http.Handler
}

// NewRouter creates a new router
func NewRouter(completion http.Handler) *Router {
	return &Router{
		completion: completion,
	}
}

// ServeHTTP implements http.Handler
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/v1/completions":
		r.completion.ServeHTTP(w, req)
	case "/health":
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	default:
		http.NotFound(w, req)
	}
}

// Server represents the HTTP server
type Server struct {
	httpServer *http.Server
}

// NewServer creates a new server instance
func NewServer(cfg config.ServerConfig, handler http.Handler) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:           fmt.Sprintf(":%d", cfg.Port),
			Handler:        handler,
			ReadTimeout:    cfg.ReadTimeout,
			WriteTimeout:   cfg.WriteTimeout,
			MaxHeaderBytes: cfg.MaxHeaderBytes,
		},
	}
}

// Start starts the server and blocks until shutdown
func (s Server) Start(ctx context.Context) error {
	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		log.Printf("Server started on port %s", s.httpServer.Addr)
		errCh <- s.httpServer.ListenAndServe()
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		// Create shutdown context with timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	// Create an LLM client with default provider
	llm, err := gollm.NewLLM(gollm.SetProvider("ollama"))
	if err != nil {
		log.Fatalf("Failed to initialize LLM: %v", err)
	}

	// Create completion handler
	completionHandler := NewCompletionHandler(llm)

	// Create router
	router := NewRouter(completionHandler)

	// Create and start server
	cfg := config.DefaultConfig()
	server := NewServer(cfg.Server, router)

	if err := server.Start(context.Background()); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
