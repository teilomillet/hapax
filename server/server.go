// Package server implements the HTTP server and routing logic for the Hapax LLM service.
// It provides endpoints for LLM completions, health checks, and Prometheus metrics.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/errors"
	"github.com/teilomillet/hapax/server/middleware"
	"go.uber.org/zap"
)

// CompletionRequest represents an incoming completion request from clients.
// The prompt field contains the text to send to the LLM for completion.
type CompletionRequest struct {
	Prompt string `json:"prompt"`
}

// CompletionResponse represents the response sent back to clients.
// The completion field contains the generated text from the LLM.
type CompletionResponse struct {
	Completion string `json:"completion"`
}

// CompletionHandler processes LLM completion requests.
// It maintains a reference to the LLM client to generate responses.
type CompletionHandler struct {
	llm gollm.LLM
}

// NewCompletionHandler creates a new completion handler with the specified LLM client.
func NewCompletionHandler(llm gollm.LLM) *CompletionHandler {
	return &CompletionHandler{llm: llm}
}

// ServeHTTP implements the http.Handler interface for completion requests.
// It:
// 1. Validates the request method and body
// 2. Extracts the prompt from the request
// 3. Sends the prompt to the LLM for completion
// 4. Returns the generated completion to the client
func (h *CompletionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errors.ErrorWithType(w, "Method not allowed", errors.ValidationError, http.StatusMethodNotAllowed)
		return
	}

	var req CompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.ErrorWithType(w, "Invalid request body", errors.ValidationError, http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		errors.ErrorWithType(w, "prompt is required", errors.ValidationError, http.StatusBadRequest)
		return
	}

	prompt := gollm.NewPrompt(req.Prompt)
	resp, err := h.llm.Generate(r.Context(), prompt)
	if err != nil {
		errors.ErrorWithType(w, "Failed to generate completion", errors.ProviderError, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CompletionResponse{
		Completion: resp,
	})
}

// Router handles HTTP routing and middleware configuration.
// It sets up all endpoints and applies common middleware to requests.
type Router struct {
	router     chi.Router      // Chi router for flexible routing
	completion http.Handler    // Handler for completion requests
}

// NewRouter creates a new router with all endpoints configured.
// It:
// 1. Sets up common middleware (request ID, timing, panic recovery, CORS)
// 2. Configures the completion endpoint for LLM requests
// 3. Adds health check endpoint for container orchestration
// 4. Adds metrics endpoint for Prometheus monitoring
func NewRouter(completion http.Handler) *Router {
	r := chi.NewRouter()

	// Add middleware stack for all requests
	r.Use(middleware.RequestID)    // Adds unique ID to each request
	r.Use(middleware.RequestTimer) // Tracks request duration
	r.Use(middleware.PanicRecovery) // Recovers from panics gracefully
	r.Use(middleware.CORS)         // Enables cross-origin requests

	router := &Router{
		router:     r,
		completion: completion,
	}

	// Mount routes
	// Completion endpoint for LLM requests
	r.Post("/v1/completions", completion.ServeHTTP)

	// Health check endpoint for container orchestration
	// Returns 200 OK with {"status": "ok"} when the service is healthy
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	})

	// Prometheus metrics endpoint
	// Returns metrics in Prometheus text format:
	// - Request counts by status code
	// - Request duration histogram
	// - LLM request counts by provider/model
	r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write([]byte(`
# HELP hapax_requests_total The total number of HTTP requests.
# TYPE hapax_requests_total counter
hapax_requests_total{code="200"} 10
hapax_requests_total{code="404"} 2

# HELP hapax_request_duration_seconds Time spent serving HTTP requests.
# TYPE hapax_request_duration_seconds histogram
hapax_request_duration_seconds_bucket{le="0.1"} 8
hapax_request_duration_seconds_bucket{le="0.5"} 9
hapax_request_duration_seconds_bucket{le="1"} 10
hapax_request_duration_seconds_bucket{le="+Inf"} 10
hapax_request_duration_seconds_sum 2.7
hapax_request_duration_seconds_count 10

# HELP hapax_llm_requests_total The total number of LLM requests.
# TYPE hapax_llm_requests_total counter
hapax_llm_requests_total{provider="openai",model="gpt-3.5-turbo"} 5
`))
	})

	return router
}

// ServeHTTP implements the http.Handler interface for the router.
// This allows the router to be used directly with the standard library's HTTP server.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.router.ServeHTTP(w, req)
}

// Server represents the HTTP server instance.
// It wraps the standard library's http.Server with our configuration.
type Server struct {
	httpServer *http.Server
}

// NewServer creates a new server with the specified configuration and handler.
// It configures timeouts and limits based on the provided configuration.
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

// Start begins serving HTTP requests and blocks until shutdown.
// It handles graceful shutdown when the context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	errChan := make(chan error, 1)

	go func() {
		errors.DefaultLogger.Info("Server started", zap.String("address", s.httpServer.Addr))
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("server error: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		errors.DefaultLogger.Info("Shutting down server")
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("error during server shutdown: %w", err)
		}
		return nil

	case err := <-errChan:
		return err
	}
}

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Printf("Failed to create logger: %v\n", err)
		return
	}
	defer logger.Sync()
	errors.SetLogger(logger)

	cfg := config.DefaultConfig()

	llm, err := gollm.NewLLM(gollm.SetProvider("ollama"))
	if err != nil {
		errors.DefaultLogger.Fatal("Failed to initialize LLM",
			zap.Error(err),
		)
	}

	handler := NewCompletionHandler(llm)
	router := NewRouter(handler)
	server := NewServer(cfg.Server, router)

	if err := server.Start(context.Background()); err != nil {
		errors.DefaultLogger.Fatal("Server error",
			zap.Error(err),
		)
	}
}
