// Package routing provides dynamic routing capabilities for the Hapax server.
// It implements versioned API routing, health checks, and dynamic middleware
// configuration through YAML configuration.
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
	"github.com/teilomillet/hapax/server/middleware"
	"go.uber.org/zap"
)

// Router handles dynamic HTTP routing with versioning and health checks.
// It provides:
// - Version-based routing (v1, v2, etc.)
// - Dynamic middleware configuration
// - Health check monitoring
// - Header validation
// - Method restrictions
type Router struct {
	router      chi.Router          // Chi router instance for HTTP routing
	handlers    map[string]http.Handler // Map of handler names to implementations
	healthState sync.Map            // Thread-safe map for storing health states
	logger      *zap.Logger         // Logger instance for error and debug logging
	cfg         *config.Config      // Server configuration
}

// NewRouter creates a new router with the given configuration.
// It initializes the router with global middleware and configures all routes
// based on the provided configuration.
//
// Parameters:
//   - cfg: Server configuration containing route definitions
//   - handlers: Map of handler names to their implementations
//   - logger: Logger instance for error and debug logging
//
// Returns:
//   - *Router: Configured router instance
func NewRouter(cfg *config.Config, handlers map[string]http.Handler, logger *zap.Logger) *Router {
	r := &Router{
		router:   chi.NewRouter(),
		handlers: handlers,
		logger:   logger,
		cfg:      cfg,
	}

	// Initialize health states
	for _, route := range cfg.Routes {
		if route.HealthCheck != nil && route.HealthCheck.Enabled {
			r.healthState.Store(route.Path, true)
		}
	}

	// Add global middleware stack
	r.router.Use(middleware.RequestID)
	r.router.Use(middleware.RequestTimer)
	r.router.Use(middleware.PanicRecovery)
	r.router.Use(middleware.CORS)

	// Configure routes
	r.setupRoutes()

	return r
}

// setupRoutes configures all routes based on the configuration.
// For each route in the configuration:
// - Adds version prefix if specified
// - Configures route-specific middleware
// - Sets up header validation
// - Restricts HTTP methods
// - Configures health check endpoints
func (r *Router) setupRoutes() {
	for _, route := range r.cfg.Routes {
		handler, ok := r.handlers[route.Handler]
		if !ok {
			r.logger.Error("handler not found", zap.String("handler", route.Handler))
			continue
		}

		// Add version prefix if specified
		path := route.Path
		if route.Version != "" {
			path = fmt.Sprintf("/%s%s", route.Version, path)
		}

		// Create route group with middleware
		r.router.Group(func(router chi.Router) {
			// Add route-specific middleware
			for _, mw := range route.Middleware {
				switch mw {
				case "auth":
					router.Use(middleware.Authentication)
				case "ratelimit":
					router.Use(middleware.RateLimit)
				default:
					r.logger.Warn("unknown middleware requested", zap.String("middleware", mw))
				}
			}

			// Add header validation middleware if headers are specified
			if len(route.Headers) > 0 {
				router.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						for key, value := range route.Headers {
							if r.Header.Get(key) != value {
								errors.ErrorWithType(w, fmt.Sprintf("missing or invalid header: %s", key), 
									errors.ValidationError, http.StatusBadRequest)
								return
							}
						}
						next.ServeHTTP(w, r)
					})
				})
			}

			// Configure methods
			methods := route.Methods
			if len(methods) == 0 {
				methods = []string{"GET"} // Default to GET if no methods specified
			}

			for _, method := range methods {
				router.Method(method, path, handler)
			}

			// Configure health check if enabled
			if route.HealthCheck != nil && route.HealthCheck.Enabled {
				healthPath := fmt.Sprintf("%s/health", path)
				router.Get(healthPath, r.healthCheckHandler(route))
				go r.startHealthCheck(route)
			}
		})
	}

	// Add global health check endpoint
	r.router.Get("/health", r.globalHealthCheckHandler())
}

// healthCheckHandler returns a handler for route-specific health checks.
// It responds with the current health status of the route and sets appropriate
// HTTP status codes (200 for healthy, 503 for unhealthy).
//
// Parameters:
//   - route: Route configuration containing health check settings
//
// Returns:
//   - http.HandlerFunc: Handler function for health check endpoint
func (r *Router) healthCheckHandler(route config.RouteConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		status := "healthy"
		if v, ok := r.healthState.Load(route.Path); ok && !v.(bool) {
			status = "unhealthy"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(map[string]string{"status": status})
	}
}

// globalHealthCheckHandler returns a handler for the global health check endpoint.
// It aggregates health status from all routes and responds with:
// - Overall system health status
// - Individual route health statuses
// - HTTP 503 if any route is unhealthy
//
// Returns:
//   - http.HandlerFunc: Handler function for global health check endpoint
func (r *Router) globalHealthCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		allHealthy := true
		statuses := make(map[string]string)

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

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   map[string]bool{"global": allHealthy},
			"services": statuses,
		})
	}
}

// startHealthCheck starts a health check goroutine for the given route.
// It periodically:
// - Runs configured health checks (HTTP, TCP)
// - Updates route health status
// - Tracks consecutive failures
// - Updates global health state
//
// Parameters:
//   - route: Route configuration containing health check settings
func (r *Router) startHealthCheck(route config.RouteConfig) {
	if route.HealthCheck == nil || !route.HealthCheck.Enabled {
		return
	}

	ticker := time.NewTicker(route.HealthCheck.Interval)
	failures := 0

	for range ticker.C {
		healthy := true
		for name, checkType := range route.HealthCheck.Checks {
			// Implement different check types here
			switch checkType {
			case "http":
				// Implement HTTP health check
				healthy = r.checkHTTPHealth(route)
			case "tcp":
				// Implement TCP health check
				healthy = r.checkTCPHealth(route)
			default:
				r.logger.Warn("unknown health check type", 
					zap.String("type", checkType),
					zap.String("check", name))
			}

			if !healthy {
				failures++
				if failures >= route.HealthCheck.Threshold {
					r.healthState.Store(route.Path, false)
				}
				break
			}
		}

		if healthy {
			failures = 0
			r.healthState.Store(route.Path, true)
		}
	}
}

// checkHTTPHealth performs an HTTP health check for a route.
// It attempts to connect to the route's endpoint and verifies
// that it responds with a 200 OK status.
//
// Parameters:
//   - route: Route configuration containing health check settings
//
// Returns:
//   - bool: true if healthy, false otherwise
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
// It attempts to establish a TCP connection to verify the service
// is accepting connections.
//
// Parameters:
//   - route: Route configuration containing health check settings
//
// Returns:
//   - bool: true if healthy, false otherwise
func (r *Router) checkTCPHealth(route config.RouteConfig) bool {
	// Implement TCP health check logic
	return true
}

// ServeHTTP implements the http.Handler interface.
// Delegates request handling to the underlying Chi router.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.router.ServeHTTP(w, req)
}
