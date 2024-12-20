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
type Router struct {
	router      chi.Router              // Chi router instance for HTTP routing
	handlers    map[string]http.Handler // Map of handler names to implementations
	healthState sync.Map                // Thread-safe map for storing health states
	logger      *zap.Logger             // Logger instance for error and debug logging
	cfg         *config.Config          // Server configuration
	metrics     *metrics.Metrics        // Metrics instance for monitoring
}

// NewRouter creates a new router with the given configuration.
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

// setupRoutes configures all routes based on the configuration.
func (r *Router) setupRoutes() {
	// Add global middleware
	r.router.Use(middleware.PrometheusMetrics(r.metrics))

	// Configure routes from config
	for _, route := range r.cfg.Routes {
		// Get handler
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
					router.Use(middleware.RateLimit(r.metrics))
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
						next.ServeHTTP(w, r)
					})
				})
			}

			// Handle methods
			if len(route.Methods) > 0 {
				for _, method := range route.Methods {
					router.Method(method, path, handler)
				}
			} else {
				router.Handle(path, handler)
			}

			// Configure health check if enabled
			if route.HealthCheck != nil {
				healthPath := fmt.Sprintf("%s/health", path)
				router.Get(healthPath, r.healthCheckHandler(route))
				r.startHealthCheck(route)
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
		json.NewEncoder(w).Encode(map[string]string{"status": status})
	}
}

// globalHealthCheckHandler returns a handler for the global health check endpoint.
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
func (r *Router) startHealthCheck(route config.RouteConfig) {
	if route.HealthCheck == nil {
		return
	}

	// Initialize health state
	r.healthState.Store(route.Path, true)

	// Start health check goroutine
	go func() {
		ticker := time.NewTicker(route.HealthCheck.Interval)
		failures := 0

		for range ticker.C {
			healthy := true

			// Perform health checks
			for name, checkType := range route.HealthCheck.Checks {
				switch checkType {
				case "http":
					healthy = r.checkHTTPHealth(route)
				case "tcp":
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
	}()
}

// checkHTTPHealth performs an HTTP health check for a route.
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
func (r *Router) checkTCPHealth(route config.RouteConfig) bool {
	// Implement TCP health check logic here
	return true
}

// ServeHTTP implements the http.Handler interface.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.router.ServeHTTP(w, req)
}
