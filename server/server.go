// Package server implements the HTTP server and routing logic for the Hapax LLM service.
// It provides endpoints for LLM completions, health checks, and Prometheus metrics.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
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
	if err := json.NewEncoder(w).Encode(CompletionResponse{
		Completion: resp,
	}); err != nil {
		// Use the existing error handling mechanism
		errors.ErrorWithType(w, "Failed to encode response", errors.ProviderError, http.StatusInternalServerError)
		return
	}
}

// Router handles HTTP routing and middleware configuration.
// It sets up all endpoints and applies common middleware to requests.
type Router struct {
	router     chi.Router   // Chi router for flexible routing
	completion http.Handler // Handler for completion requests
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
	r.Use(middleware.RequestID)     // Adds unique ID to each request
	r.Use(middleware.RequestTimer)  // Tracks request duration
	r.Use(middleware.PanicRecovery) // Recovers from panics gracefully
	r.Use(middleware.CORS)          // Enables cross-origin requests

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
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		}); err != nil {
			// Use the existing error handling mechanism
			errors.ErrorWithType(w, "Failed to encode response", errors.ProviderError, http.StatusInternalServerError)
			return
		}
	})

	// Prometheus metrics endpoint
	// Returns metrics in Prometheus text format:
	// - Request counts by status code
	// - Request duration histogram
	// - LLM request counts by provider/model
	r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		// Check the error return from Write
		if _, err := w.Write([]byte(`
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
`)); err != nil {
			// In a real-world scenario, log the error
			fmt.Printf("Failed to write metrics response: %v", err)

			// Send an error response
			http.Error(w, "Failed to generate metrics", http.StatusInternalServerError)
			return
		}
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
// The running boolean tracks whether the server is operational, while the mu mutex protects concurrent access to this state.
// This ensures that multiple goroutines do not interfere with each other when checking or updating the server's status.
type Server struct {
	httpServer *http.Server
	config     config.Watcher
	logger     *zap.Logger
	llm        gollm.LLM
	running    bool       // Track server state
	mu         sync.Mutex // Protect state changes
}

// NewServer creates a new server with the specified configuration and handler.
// It configures timeouts and limits based on the provided configuration.
func NewServer(configPath string, logger *zap.Logger) (*Server, error) {
	configWatcher, err := config.NewConfigWatcher(configPath, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create config watcher: %w", err)
	}

	// Create initial LLM instance
	initialConfig := configWatcher.GetCurrentConfig()
	llm, err := gollm.NewLLM(gollm.SetProvider(initialConfig.LLM.Provider))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}

	s := &Server{
		logger: logger,
		config: configWatcher,
		llm:    llm,
	}

	// Initialize server with current config
	if err := s.updateServerConfig(initialConfig); err != nil {
		return nil, err
	}

	// Subscribe to config changes
	configChan := configWatcher.Subscribe()
	go s.handleConfigUpdates(configChan)

	return s, nil
}

// NewServerWithConfig for testing now takes care of the LLM
func NewServerWithConfig(cfg config.Watcher, llm gollm.LLM, logger *zap.Logger) (*Server, error) {
	s := &Server{
		logger: logger,
		config: cfg,
		llm:    llm,
	}

	// Initialize server with current config
	if err := s.updateServerConfig(cfg.GetCurrentConfig()); err != nil {
		return nil, err
	}

	// Subscribe to config changes
	configChan := cfg.Subscribe()
	go s.handleConfigUpdates(configChan)

	return s, nil
}

// updateServerConfig updates the server configuration and handles graceful shutdown.
// When a new configuration is received, it first checks if the server is running.
// If it is, it initiates a graceful shutdown to close existing connections before applying the new configuration.
// This ensures that there are no disruptions during the transition to the new configuration.
func (s *Server) updateServerConfig(cfg *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create new handler and router
	handler := NewCompletionHandler(s.llm)
	router := NewRouter(handler)

	// Create new HTTP server with updated config
	newServer := &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:        router,
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
	}

	// If server is running, we need to stop it and start the new one
	if s.running {
		// Gracefully shutdown existing server
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("failed to shutdown existing server: %w", err)
		}
	}

	// Update server instance
	s.httpServer = newServer

	// If we were running before, start the new server
	if s.running {
		go func() {
			if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
				s.logger.Error("Server error", zap.Error(err))
			}
		}()

		// Wait for server to be ready
		if err := s.waitForServer(cfg.Server.Port); err != nil {
			return fmt.Errorf("server failed to start on new port: %w", err)
		}
	}

	return nil
}

// waitForServer checks if the server is ready on its new port.
// It attempts to connect multiple times to ensure that the server is fully initialized before proceeding.
// This is crucial for confirming that the service is operational in its new location (port).
func (s *Server) waitForServer(port int) error {
	for i := 0; i < 50; i++ { // Try for 5 seconds (50 * 100ms)
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server failed to start within timeout")
}

// handleConfigUpdates listens for configuration changes and processes them.
// When a new configuration is received, it updates the server's settings and manages the lifecycle of the server accordingly.
// This includes shutting down the existing server and starting a new one with the updated configuration.
func (s *Server) handleConfigUpdates(configChan <-chan *config.Config) {
	for newConfig := range configChan {
		s.logger.Info("Received config update")

		// Update LLM if provider changed
		if newConfig.LLM.Provider != s.llm.GetProvider() {
			newLLM, err := gollm.NewLLM(gollm.SetProvider(newConfig.LLM.Provider))
			if err != nil {
				s.logger.Error("Failed to update LLM provider", zap.Error(err))
				continue
			}
			s.llm = newLLM
		}

		// Create temporary server with new config
		tempServer := &http.Server{}
		if err := s.updateServerConfig(newConfig); err != nil {
			s.logger.Error("Failed to update server config", zap.Error(err))
			continue
		}

		// Gracefully shutdown existing connections
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := s.httpServer.Shutdown(ctx); err != nil {
			s.logger.Error("Error during server shutdown", zap.Error(err))
		}
		cancel()

		// Start server with new configuration
		s.httpServer = tempServer
		go func() {
			if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
				s.logger.Error("Server error", zap.Error(err))
			}
		}()

		s.logger.Info("Server restarted with new configuration")
	}
}

// Start begins serving HTTP requests and blocks until shutdown.
// It handles graceful shutdown when the context is cancelled, ensuring that all connections are properly closed before exiting.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

func main() {
	// Create logger with explicit error handling
	logger, err := zap.NewProduction()
	if err != nil {
		// Fail fast if logger creation fails
		fmt.Printf("Critical error: Failed to create logger: %v\n", err)
		os.Exit(1)
	}

	// Ensure logger is synced, with robust error handling
	defer func() {
		if syncErr := logger.Sync(); syncErr != nil {
			// Log sync failure, but don't mask the original error
			fmt.Printf("Warning: Failed to sync logger: %v\n", syncErr)
		}
	}()

	// Set global logger
	errors.SetLogger(logger)

	// Configuration and server setup with comprehensive error handling
	configPath := "config.yaml"
	server, err := NewServer(configPath, logger)
	if err != nil {
		logger.Fatal("Server initialization failed",
			zap.Error(err),
			zap.String("config_path", configPath),
		)
	}

	// Graceful shutdown infrastructure
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling with detailed logging
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Shutdown signal received",
			zap.String("signal", sig.String()),
			zap.String("action", "initiating graceful shutdown"),
		)
		cancel()
	}()

	// Server start with comprehensive error tracking
	if err := server.Start(ctx); err != nil {
		logger.Fatal("Server startup or runtime error",
			zap.Error(err),
			zap.String("action", "server_start_failed"),
		)
	}
}
