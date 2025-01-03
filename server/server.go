// Package server implements the HTTP server and routing logic for the Hapax LLM service.
// It provides endpoints for LLM completions, health checks, and Prometheus metrics.
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
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
	httpServer  *http.Server
	http3Server *http3.Server // HTTP/3 server instance
	config      config.Watcher
	logger      *zap.Logger
	llm         gollm.LLM
	running     bool       // Track server state
	mu          sync.Mutex // Protect state changes
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

	// Create new HTTP server instance
	newServer := &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:        NewRouter(s.llm, cfg, s.logger),
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
	}

	// Create new HTTP/3 server if enabled
	var newHTTP3Server *http3.Server
	if cfg.Server.HTTP3.Enabled {
		quicConfig := &quic.Config{
			MaxStreamReceiveWindow:     cfg.Server.HTTP3.MaxStreamReceiveWindow,
			MaxConnectionReceiveWindow: cfg.Server.HTTP3.MaxConnectionReceiveWindow,
			MaxIncomingStreams:         cfg.Server.HTTP3.MaxBiStreamsConcurrent,
			MaxIncomingUniStreams:      cfg.Server.HTTP3.MaxUniStreamsConcurrent,
			KeepAlivePeriod:            cfg.Server.HTTP3.IdleTimeout / 2,
			Allow0RTT:                  cfg.Server.HTTP3.Enable0RTT,
		}

		// If 0-RTT is enabled but replay is not allowed, wrap the handler
		var http3Handler http.Handler = NewRouter(s.llm, cfg, s.logger)
		if cfg.Server.HTTP3.Enable0RTT && !cfg.Server.HTTP3.Allow0RTTReplay {
			http3Handler = &replayProtectionHandler{
				handler: NewRouter(s.llm, cfg, s.logger),
				logger:  s.logger,
				seen:    sync.Map{},
				maxSize: cfg.Server.HTTP3.Max0RTTSize,
				enabled: cfg.Server.HTTP3.Enable0RTT,
				allowed: cfg.Server.HTTP3.Allow0RTTReplay,
			}
		}

		newHTTP3Server = &http3.Server{
			Addr:       fmt.Sprintf(":%d", cfg.Server.HTTP3.Port),
			Handler:    http3Handler,
			QUICConfig: quicConfig,
		}

		// Configure UDP receive buffer size
		if cfg.Server.HTTP3.UDPReceiveBufferSize > 0 {
			if err := configureUDPBufferSize(cfg.Server.HTTP3.UDPReceiveBufferSize); err != nil {
				s.logger.Warn("Failed to set UDP receive buffer size", zap.Error(err))
			}
		}
	}

	wasRunning := s.running
	if wasRunning {
		// Gracefully shutdown existing server
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("failed to shutdown existing server: %w", err)
		}

		// Shutdown HTTP/3 server if it exists
		http3Server := s.http3Server
		if http3Server != nil {
			if err := http3Server.Close(); err != nil {
				s.logger.Error("Failed to close HTTP/3 server", zap.Error(err))
			}
		}
		s.running = false
	}

	// Update server instances
	s.httpServer = newServer
	s.http3Server = newHTTP3Server

	// If we were running before, start the new server
	if wasRunning {
		s.running = true
		go func() {
			if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
				s.logger.Error("HTTP server error", zap.Error(err))
			}
		}()

		// IMPORTANT: Concurrent Access Pattern
		// This pattern of separate declaration and assignment is intentional:
		// 1. We need a stable reference to the HTTP/3 server that won't change
		//    during the goroutine's lifetime, even if s.http3Server is modified
		// 2. The separate declaration makes it clear we're capturing state
		//    that will be used concurrently
		// 3. This prevents potential race conditions where s.http3Server might
		//    be modified while the goroutine is starting up
		//nolint:gosimple // Separate declaration maintains clear capture semantics for concurrent access
		var http3Server *http3.Server
		http3Server = s.http3Server
		if http3Server != nil {
			// The goroutine captures the local http3Server variable,
			// ensuring it has a consistent view of the server state
			go func() {
				if err := http3Server.ListenAndServeTLS(
					cfg.Server.HTTP3.TLSCertFile,
					cfg.Server.HTTP3.TLSKeyFile,
				); err != http.ErrServerClosed {
					s.logger.Error("HTTP/3 server error", zap.Error(err))
				}
			}()
		}

		// Wait for server to be ready
		if err := s.waitForServer(cfg.Server.Port); err != nil {
			return fmt.Errorf("server failed to start on new port: %w", err)
		}

		// Wait for HTTP/3 server if enabled
		if http3Server != nil {
			if err := s.waitForServer(cfg.Server.HTTP3.Port); err != nil {
				return fmt.Errorf("HTTP/3 server failed to start on new port: %w", err)
			}
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

		// Update server configuration
		if err := s.updateServerConfig(newConfig); err != nil {
			s.logger.Error("Failed to update server config", zap.Error(err))
			continue
		}

		s.logger.Info("Server restarted with new configuration")
	}
}

// Start begins serving HTTP requests and blocks until shutdown.
// It handles graceful shutdown when the context is cancelled, ensuring that all connections are properly closed before exiting.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server is already running")
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	// Create error channel for server errors
	errChan := make(chan error, 2) // Buffer for both HTTP and HTTP/3 errors

	// Start HTTP server
	go func() {
		if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Start HTTP/3 server if enabled
	s.mu.Lock()
	http3Server := s.http3Server
	s.mu.Unlock()

	if http3Server != nil {
		cfg := s.config.GetCurrentConfig()
		go func() {
			if err := http3Server.ListenAndServeTLS(
				cfg.Server.HTTP3.TLSCertFile,
				cfg.Server.HTTP3.TLSKeyFile,
			); err != http.ErrServerClosed {
				errChan <- fmt.Errorf("HTTP/3 server error: %w", err)
			}
		}()
	}

	// Wait for context cancellation or server error
	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		// Graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.GetCurrentConfig().Server.ShutdownTimeout)
		defer cancel()

		// Shutdown HTTP server
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("Error during HTTP server shutdown", zap.Error(err))
		}

		// Shutdown HTTP/3 server if it exists
		s.mu.Lock()
		http3Server := s.http3Server
		s.mu.Unlock()

		if http3Server != nil {
			if err := http3Server.Close(); err != nil {
				s.logger.Error("Error during HTTP/3 server shutdown", zap.Error(err))
			}
		}

		return nil
	}
}

// configureUDPBufferSize attempts to set the UDP receive buffer size
func configureUDPBufferSize(size uint32) error {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		return fmt.Errorf("create test UDP connection: %w", err)
	}
	defer conn.Close()

	return conn.SetReadBuffer(int(size))
}

// replayProtectionHandler implements replay protection for POST requests
type replayProtectionHandler struct {
	handler http.Handler
	logger  *zap.Logger
	seen    sync.Map
	maxSize uint32
	enabled bool
	allowed bool
}

func (h *replayProtectionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only apply replay protection to POST requests
	if r.Method == http.MethodPost && h.enabled && !h.allowed {
		// Calculate request hash (URL + headers + body)
		hash, err := h.calculateRequestHash(r)
		if err != nil {
			h.logger.Error("Failed to calculate request hash", zap.Error(err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Check if we've seen this request before
		if _, loaded := h.seen.LoadOrStore(hash, time.Now()); loaded {
			h.logger.Debug("Rejected replayed request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("hash", hash))
			http.Error(w, "Request replay not allowed", http.StatusTooEarly)
			return
		}

		// Clean up old entries periodically (could be moved to a background task)
		h.cleanupOldEntries()
	}

	h.handler.ServeHTTP(w, r)
}

func (h *replayProtectionHandler) calculateRequestHash(r *http.Request) (string, error) {
	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Create hash of URL + headers + body
	hasher := sha256.New()
	hasher.Write([]byte(r.URL.String()))

	// Add selected headers to hash
	headers := []string{"Content-Type", "Authorization"}
	for _, header := range headers {
		hasher.Write([]byte(r.Header.Get(header)))
	}

	hasher.Write(body)
	return base64.StdEncoding.EncodeToString(hasher.Sum(nil)), nil
}

func (h *replayProtectionHandler) cleanupOldEntries() {
	now := time.Now()
	h.seen.Range(func(key, value interface{}) bool {
		if timestamp, ok := value.(time.Time); ok {
			// Remove entries older than 5 minutes
			if now.Sub(timestamp) > 5*time.Minute {
				h.seen.Delete(key)
			}
		}
		return true
	})
}
