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

// Router handles HTTP routing
type Router struct {
	router     chi.Router
	completion http.Handler
}

// NewRouter creates a new router
func NewRouter(completion http.Handler) *Router {
	r := chi.NewRouter()

	// Add our middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RequestTimer)
	r.Use(middleware.PanicRecovery)
	r.Use(middleware.CORS)

	router := &Router{
		router:     r,
		completion: completion,
	}

	// Mount routes
	r.Post("/v1/completions", completion.ServeHTTP)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	})

	return router
}

// ServeHTTP implements http.Handler
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.router.ServeHTTP(w, req)
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
