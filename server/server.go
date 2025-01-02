// Package server implements the HTTP server and routing logic for the Hapax LLM service.
// It provides endpoints for LLM completions, health checks, and Prometheus metrics.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/errors"
	"github.com/teilomillet/hapax/server/handlers"
	"github.com/teilomillet/hapax/server/metrics"
	"github.com/teilomillet/hapax/server/middleware"
	"github.com/teilomillet/hapax/server/processing"
	"go.uber.org/zap"
)

// Router handles HTTP routing and middleware configuration.
// It sets up all endpoints and applies common middleware to requests.
type Router struct {
	router     chi.Router       // Chi router for flexible routing
	completion http.Handler     // Handler for completion requests
	metrics    *metrics.Metrics // Server metrics
}

// NewRouter creates a new router with all endpoints configured.
// It:
// 1. Sets up common middleware (request ID, timing, panic recovery, CORS)
// 2. Configures the completion endpoint for LLM requests
// 3. Adds health check endpoint for container orchestration
// 4. Adds metrics endpoint for Prometheus monitoring
func NewRouter(llm gollm.LLM, cfg *config.Config, logger *zap.Logger) *Router {
	r := chi.NewRouter()

	// Initialize metrics
	m := metrics.NewMetrics()

	// Add middleware stack for all requests
	r.Use(middleware.RequestID) // Adds unique ID to each request

	// Configure queue middleware if enabled
	if cfg.Queue.Enabled {
		qm := middleware.NewQueueMiddleware(middleware.QueueConfig{
			InitialSize:  cfg.Queue.InitialSize,
			Metrics:      m,
			StatePath:    cfg.Queue.StatePath,
			SaveInterval: cfg.Queue.SaveInterval,
		})
		r.Use(qm.Handler)
	}

	r.Use(middleware.RequestTimer)  // Tracks request duration
	r.Use(middleware.PanicRecovery) // Recovers from panics gracefully
	r.Use(middleware.CORS)          // Enables cross-origin requests

	// Create processor for the completion handler
	processingCfg := &config.ProcessingConfig{
		RequestTemplates: map[string]string{
			"default":  "{{.Input}}",
			"chat":     "{{range .Messages}}{{.Role}}: {{.Content}}\n{{end}}",
			"function": "Function: {{.FunctionDescription}}\nInput: {{.Input}}",
		},
	}

	processor, err := processing.NewProcessor(processingCfg, llm)
	if err != nil {
		logger.Fatal("Failed to create processor", zap.Error(err))
	}

	// Create new completion handler using the handlers package
	completionHandler := handlers.NewCompletionHandler(processor, logger)

	router := &Router{
		router:     r,
		completion: completionHandler,
		metrics:    m,
	}

	// Mount routes
	// Completion endpoint for LLM requests
	r.Post("/v1/completions", completionHandler.ServeHTTP)

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
	r.Handle("/metrics", m.Handler())

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
	handler := NewRouter(s.llm, cfg, s.logger)

	// Create new HTTP server with updated config
	newServer := &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:        handler,
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
