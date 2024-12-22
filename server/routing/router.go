// Package routing provides dynamic HTTP routing with versioning and health checks.
package routing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/errors"
	"github.com/teilomillet/hapax/server/metrics"
	"github.com/teilomillet/hapax/server/middleware"
	"go.uber.org/zap"
)

// Router handles dynamic HTTP routing with versioning and health checks.
// It utilizes chi for routing and provides middleware support for metrics and authentication.
type Router struct {
	router      chi.Router              // Chi router instance for HTTP routing
	handlers    map[string]http.Handler // Map of handler names to implementations
	healthState sync.Map                // Thread-safe map for storing health states
	logger      *zap.Logger             // Logger instance for error and debug logging
	cfg         *config.Config          // Server configuration
	metrics     *metrics.Metrics        // Metrics instance for monitoring
}

// NewRouter creates a new router with the given configuration and initializes routes.
func NewRouter(cfg *config.Config, handlers map[string]http.Handler, logger *zap.Logger, metrics *metrics.Metrics) *Router {
	r := &Router{
		router:   chi.NewRouter(),
		handlers: handlers,
		logger:   logger,
		cfg:      cfg,
		metrics:  metrics,
	}

	// Configure routes
	r.setupRoutes()

	return r
}

// setupRoutes configures all routes based on the configuration provided in the server config.
func (r *Router) setupRoutes() {
	// Add global middleware for metrics monitoring
	r.router.Use(middleware.PrometheusMetrics(r.metrics))

	// Configure routes from config
	for _, route := range r.cfg.Routes {
		// Get handler for the route
		handler, ok := r.handlers[route.Handler]
		if !ok {
			r.logger.Error("handler not found", zap.String("handler", route.Handler))
			continue
		}

		// Add version prefix to the path if specified
		path := route.Path
		if route.Version != "" {
			path = fmt.Sprintf("/%s%s", route.Version, path)
		}

		// Create route group with specified middleware
		r.router.Group(func(router chi.Router) {
			// Add route-specific middleware
			for _, mw := range route.Middleware {
				switch mw {
				case "auth":
					router.Use(middleware.Authentication) // Add authentication middleware
				case "ratelimit":
					router.Use(middleware.RateLimit(r.metrics)) // Add rate limiting middleware
				default:
					r.logger.Warn("unknown middleware requested", zap.String("middleware", mw))
				}
			}

			// Add header validation middleware if headers are specified
			if len(route.Headers) > 0 {
				router.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						// Check required headers
						for header, value := range route.Headers {
							if r.Header.Get(header) != value {
								errResp := errors.NewError(
									errors.ValidationError,
									fmt.Sprintf("missing or invalid header: %s", header),
									http.StatusBadRequest,
									"",
									nil,
									nil,
								)
								errors.WriteError(w, errResp)
								return
							}
						}
						next.ServeHTTP(w, r) // Call the next handler
					})
				})
			}

			// Handle methods for the route
			if len(route.Methods) > 0 {
				for _, method := range route.Methods {
					router.Method(method, path, handler) // Register method-specific handler
				}
			} else {
				router.Handle(path, handler) // Default to handle for all methods
			}

			// Configure health check if enabled
			if route.HealthCheck != nil {
				healthPath := fmt.Sprintf("%s/health", path)
				router.Get(healthPath, r.healthCheckHandler(route)) // Register health check handler
				r.startHealthCheck(route)                           // Start health check routine
			}
		})
	}

	// Add global health check endpoint
	r.router.Get("/health", r.globalHealthCheckHandler())

	// Add metrics endpoint
	r.router.Handle("/metrics", r.metrics.Handler())
}

// healthCheckHandler returns a handler for route-specific health checks.
func (r *Router) healthCheckHandler(route config.RouteConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		status := "healthy"
		if v, ok := r.healthState.Load(route.Path); ok && !v.(bool) {
			status = "unhealthy"
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		// Properly handle potential JSON encoding errors
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": status}); err != nil {
			// Log the error and send a generic error response
			r.logger.Error("Failed to encode health check response",
				zap.String("route", route.Path),
				zap.Error(err))

			// Send a fallback error response
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
}

// globalHealthCheckHandler returns a handler for the global health check endpoint.
func (r *Router) globalHealthCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		allHealthy := true
		statuses := make(map[string]string)

		// Iterate through health states of all routes
		r.healthState.Range(func(key, value interface{}) bool {
			path := key.(string)
			healthy := value.(bool)
			if !healthy {
				allHealthy = false
				statuses[path] = "unhealthy"
			} else {
				statuses[path] = "healthy"
			}
			return true
		})

		if !allHealthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		// Properly handle potential JSON encoding errors
		w.Header().Set("Content-Type", "application/json")
		response := map[string]interface{}{
			"status":   map[string]bool{"global": allHealthy},
			"services": statuses,
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			// Log the error and send a generic error response
			r.logger.Error("Failed to encode global health check response",
				zap.Bool("all_healthy", allHealthy),
				zap.Error(err))

			// Send a fallback error response
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
}

// startHealthCheck starts a health check goroutine for the given route.
// It periodically checks the health of the route and updates the health state.
func (r *Router) startHealthCheck(route config.RouteConfig) {
	if route.HealthCheck == nil {
		return // Exit if no health check configuration
	}

	// Initialize health state
	r.healthState.Store(route.Path, true)

	// Start health check goroutine
	go func() {
		ticker := time.NewTicker(route.HealthCheck.Interval) // Set up ticker for health checks
		failures := 0

		for range ticker.C {
			healthy := true

			// Perform health checks
			for name, checkType := range route.HealthCheck.Checks {
				switch checkType {
				case "http":
					healthy = r.checkHTTPHealth(route) // Perform HTTP health check
				case "tcp":
					healthy = r.checkTCPHealth(route) // Perform TCP health check
				default:
					r.logger.Warn("unknown health check type",
						zap.String("type", checkType),
						zap.String("check", name))
				}

				if !healthy {
					failures++
					if failures >= route.HealthCheck.Threshold {
						r.healthState.Store(route.Path, false) // Mark route as unhealthy
					}
					break
				}
			}

			if healthy {
				failures = 0
				r.healthState.Store(route.Path, true)
			}
		}
	}()
}

// checkHTTPHealth performs an HTTP health check for a route.
// It returns true if the route is healthy, false otherwise.
func (r *Router) checkHTTPHealth(route config.RouteConfig) bool {
	client := &http.Client{
		Timeout: route.HealthCheck.Timeout,
	}

	resp, err := client.Get(fmt.Sprintf("http://localhost:%d%s", r.cfg.Server.Port, route.Path))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// checkTCPHealth performs a TCP health check for a route.
// It returns true if the route is healthy, false otherwise.
func (r *Router) checkTCPHealth(route config.RouteConfig) bool {
	// Implement TCP health check logic here
	return true
}

// ServeHTTP implements the http.Handler interface.
// It handles incoming HTTP requests.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.router.ServeHTTP(w, req)
}
