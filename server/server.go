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
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
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
	"golang.org/x/sys/unix"
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

	// Add replay protection to the completion handler
	replayProtection := &replayProtectionHandler{
		handler: completionHandler,
		logger:  logger,
		enabled: true,
		allowed: false,
		maxSize: 1000, // Maximum number of request hashes to store
	}

	router := &Router{
		router:     r,
		completion: replayProtection,
		metrics:    m,
	}

	// Mount routes
	// Completion endpoint for LLM requests
	r.Post("/v1/completions", replayProtection.ServeHTTP)

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
	http3Server *http3.Server
	config      config.Watcher
	logger      *zap.Logger
	llm         gollm.LLM
	running     bool
	mu          sync.RWMutex
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

	// If there's an existing server, shut it down gracefully first
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()

		if err := s.httpServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown existing server: %w", err)
		}
	}

	// If there's an existing HTTP/3 server, shut it down gracefully
	if s.http3Server != nil {
		if err := s.http3Server.Close(); err != nil {
			return fmt.Errorf("failed to shutdown existing HTTP/3 server: %w", err)
		}
	}

	// Create router
	router := NewRouter(s.llm, cfg, s.logger)

	// Create new HTTP server instance
	s.httpServer = &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:        router,
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
	}

	// Configure HTTP/3 if enabled
	if cfg.Server.HTTP3 != nil && cfg.Server.HTTP3.Enabled {
		s.http3Server = &http3.Server{
			Addr:            fmt.Sprintf(":%d", cfg.Server.HTTP3.Port),
			Handler:         router,
			MaxHeaderBytes:  cfg.Server.MaxHeaderBytes,
			EnableDatagrams: true,
			QUICConfig: &quic.Config{
				MaxIdleTimeout:             cfg.Server.HTTP3.IdleTimeout,
				MaxStreamReceiveWindow:     cfg.Server.HTTP3.MaxStreamReceiveWindow,
				MaxConnectionReceiveWindow: cfg.Server.HTTP3.MaxConnectionReceiveWindow,
				Allow0RTT:                  cfg.Server.HTTP3.Enable0RTT,
				MaxIncomingStreams:         int64(cfg.Server.HTTP3.MaxBiStreamsConcurrent),
				MaxIncomingUniStreams:      int64(cfg.Server.HTTP3.MaxUniStreamsConcurrent),
			},
		}
	}

	return nil
}

// waitForServer checks if the server is ready on its new port.
// It attempts to connect multiple times to ensure that the server is fully initialized before proceeding.
// This is crucial for confirming that the service is operational in its new location (port).
func (s *Server) waitForServer(port int) error {
	// Helper function to check if port is in use
	checkPort := func() error {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 100*time.Millisecond)
		if err != nil {
			return err
		}
		conn.Close()
		return nil
	}

	// Try to connect multiple times
	for i := 0; i < 50; i++ { // Try for 5 seconds (50 * 100ms)
		err := checkPort()
		if err == nil {
			// Port is available and server is listening
			return nil
		}

		// Check if the error indicates the port is in use by another process
		if opErr, ok := err.(*net.OpError); ok {
			if syscallErr, ok := opErr.Err.(*os.SyscallError); ok {
				if syscallErr.Syscall == "connect" {
					// Port is in use, but not by our server
					s.logger.Warn("Port is in use by another process",
						zap.Int("port", port),
						zap.Error(err))
				}
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("server failed to start on port %d within timeout", port)
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

		// Handle the configuration update in a separate function to properly scope the defer
		if err := s.applyConfigUpdate(newConfig); err != nil {
			s.logger.Error("Failed to apply config update", zap.Error(err))
			continue
		}
	}
}

// applyConfigUpdate handles the actual configuration update process.
// It manages the shutdown of existing servers and startup of new ones with updated configuration.
func (s *Server) applyConfigUpdate(newConfig *config.Config) error {
	// Create a context for the update operation
	ctx, cancel := context.WithTimeout(context.Background(), newConfig.Server.ShutdownTimeout)
	defer cancel() // This defer is now properly scoped

	// Stop the current server
	s.mu.Lock()
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			s.logger.Error("Failed to shutdown HTTP server", zap.Error(err))
		}
	}
	if s.http3Server != nil {
		if err := s.http3Server.Close(); err != nil {
			s.logger.Error("Failed to shutdown HTTP/3 server", zap.Error(err))
		}
	}
	s.httpServer = nil
	s.http3Server = nil
	s.mu.Unlock()

	// Wait for ports to be released
	time.Sleep(100 * time.Millisecond)

	// Update server configuration
	if err := s.updateServerConfig(newConfig); err != nil {
		return fmt.Errorf("failed to update server config: %w", err)
	}

	// Start the new server
	s.mu.Lock()
	httpServer := s.httpServer
	http3Server := s.http3Server
	s.mu.Unlock()

	// Start HTTP server
	go func() {
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", zap.Error(err))
		}
	}()

	// Start HTTP/3 server if enabled
	if http3Server != nil {
		go func() {
			if err := http3Server.ListenAndServeTLS(
				newConfig.Server.HTTP3.TLSCertFile,
				newConfig.Server.HTTP3.TLSKeyFile,
			); err != http.ErrServerClosed {
				s.logger.Error("HTTP/3 server error", zap.Error(err))
			}
		}()
	}

	// Wait for server to be ready
	if err := s.waitForServer(newConfig.Server.Port); err != nil {
		s.logger.Error("Server failed to start on new port", zap.Error(err))
		return err
	}

	s.logger.Info("Server restarted with new configuration",
		zap.Int("port", newConfig.Server.Port))

	return nil
}

// Start begins serving HTTP requests and blocks until shutdown.
// It handles graceful shutdown when the context is cancelled, ensuring that all connections are properly closed before exiting.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server is already running")
	}

	// Initialize server configuration if not already done
	if s.httpServer == nil {
		if err := s.updateServerConfig(s.config.GetCurrentConfig()); err != nil {
			s.mu.Unlock()
			return fmt.Errorf("failed to initialize server configuration: %w", err)
		}
	}

	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	// Create error channel for server errors
	errChan := make(chan error, 2)

	// Start HTTP server
	go func() {
		s.mu.RLock()
		httpServer := s.httpServer
		s.mu.RUnlock()

		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Configure HTTP/3 if enabled
	s.mu.RLock()
	cfg := s.config.GetCurrentConfig()
	http3Server := s.http3Server
	s.mu.RUnlock()

	if cfg.Server.HTTP3 != nil && cfg.Server.HTTP3.Enabled && http3Server != nil {
		// Try to configure UDP buffer size, but don't fail if we can't
		if cfg.Server.HTTP3.UDPReceiveBufferSize > 0 {
			if err := configureUDPBufferSize(cfg.Server.HTTP3.UDPReceiveBufferSize); err != nil {
				// Log the error but continue - the server may still work with a smaller buffer
				s.logger.Warn("Failed to configure UDP receive buffer size",
					zap.Uint32("requested_size", cfg.Server.HTTP3.UDPReceiveBufferSize),
					zap.Error(err))
			}
		}

		// Start HTTP/3 server
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
		s.mu.RLock()
		httpServer := s.httpServer
		http3Server := s.http3Server
		s.mu.RUnlock()

		// Graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.GetCurrentConfig().Server.ShutdownTimeout)
		defer cancel()

		// Create error channel for shutdown errors
		shutdownErrChan := make(chan error, 2)

		// Shutdown HTTP server
		go func() {
			if err := httpServer.Shutdown(shutdownCtx); err != nil {
				s.logger.Error("Error during HTTP server shutdown", zap.Error(err))
				shutdownErrChan <- err
			}
			shutdownErrChan <- nil
		}()

		// Shutdown HTTP/3 server if it exists
		if http3Server != nil {
			go func() {
				if err := http3Server.Close(); err != nil {
					s.logger.Error("Error during HTTP/3 server shutdown", zap.Error(err))
					shutdownErrChan <- err
				}
				shutdownErrChan <- nil
			}()
		}

		// Wait for both servers to shut down or timeout
		shutdownTimeout := time.After(s.config.GetCurrentConfig().Server.ShutdownTimeout)
		shutdownCount := 1 // Start with 1 for HTTP server
		if http3Server != nil {
			shutdownCount++ // Add 1 for HTTP/3 server
		}

		for i := 0; i < shutdownCount; i++ {
			select {
			case err := <-shutdownErrChan:
				if err != nil {
					s.logger.Error("Server shutdown error", zap.Error(err))
				}
			case <-shutdownTimeout:
				s.logger.Error("Server shutdown timed out")
				return fmt.Errorf("server shutdown timed out")
			}
		}

		// Clear server references
		s.mu.Lock()
		s.httpServer = nil
		s.http3Server = nil
		s.mu.Unlock()

		return nil
	}
}

// configureUDPBufferSize attempts to set the UDP receive buffer size
func configureUDPBufferSize(size uint32) error {
	// Try to set the buffer size with sysctl first
	if err := exec.Command("sysctl", "-w", fmt.Sprintf("net.core.rmem_max=%d", size)).Run(); err != nil {
		// Sysctl failed, which is expected in non-root environments
		// Try to set it directly on the connection
		conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
		if err != nil {
			return fmt.Errorf("create test UDP connection: %w", err)
		}
		defer conn.Close()

		// Get initial buffer size for logging
		initialSize, err := getUDPBufferSize(conn)
		if err != nil {
			return fmt.Errorf("get initial UDP buffer size: %w", err)
		}

		// Try to set the buffer size
		if err := conn.SetReadBuffer(int(size)); err != nil {
			return fmt.Errorf("set UDP read buffer (current: %d, requested: %d): %w", initialSize, size, err)
		}

		// Verify the actual buffer size
		actualSize, err := getUDPBufferSize(conn)
		if err != nil {
			return fmt.Errorf("get UDP buffer size after setting: %w", err)
		}

		// On Linux, the actual buffer size is twice the requested size
		// See: https://man7.org/linux/man-pages/man7/socket.7.html
		effectiveSize := actualSize / 2
		if effectiveSize < int(size) {
			// If we couldn't get the full size, log a warning with the actual size we got
			log.Printf("Warning: UDP receive buffer size smaller than requested (was: %d kiB, wanted: %d kiB, got: %d kiB). See https://github.com/quic-go/quic-go/wiki/UDP-Buffer-Sizes for details.",
				initialSize/1024, size/1024, effectiveSize/1024)
			return nil // Continue with the smaller buffer size
		}
	}
	return nil
}

func getUDPBufferSize(conn *net.UDPConn) (int, error) {
	f, err := conn.File()
	if err != nil {
		return 0, err
	}
	defer f.Close()

	size, err := unix.GetsockoptInt(int(f.Fd()), unix.SOL_SOCKET, unix.SO_RCVBUF)
	if err != nil {
		return 0, fmt.Errorf("getsockopt: %w", err)
	}
	return size, nil
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
