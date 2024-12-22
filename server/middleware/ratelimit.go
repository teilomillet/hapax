package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/teilomillet/hapax/errors"
	"github.com/teilomillet/hapax/server/metrics"
	"golang.org/x/time/rate"
)

// rateLimiters holds the rate limiters for each visitor IP address
// and ensures safe concurrent access using a read-write mutex.
type rateLimiters struct {
	// visitors is a map of IP addresses to their corresponding rate limiters.
	visitors map[string]*rate.Limiter
	// mu is a read-write mutex that protects access to the visitors map.
	mu sync.RWMutex
}

// limiters is a global instance of rateLimiters to manage rate limiting.
var (
	limiters = &rateLimiters{
		visitors: make(map[string]*rate.Limiter),
	}
)

// GetOrCreate retrieves the rate limiter for the given IP address,
// creating a new one if it does not exist.
func (l *rateLimiters) GetOrCreate(ip string, create func() *rate.Limiter) *rate.Limiter {
	// Lock the mutex to ensure exclusive access to the visitors map.
	l.mu.Lock()
	defer l.mu.Unlock()

	// Check if a rate limiter already exists for the given IP address.
	limiter, exists := l.visitors[ip]
	if !exists {
		// If not, create a new rate limiter using the provided create function.
		limiter = create()
		// Store the new rate limiter in the visitors map.
		l.visitors[ip] = limiter
	}

	return limiter
}

// RateLimit creates a new rate limit middleware that applies rate limiting
// to incoming requests and tracks metrics.
func RateLimit(metrics *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract the IP address from the request.
			ip := r.RemoteAddr
			if idx := strings.LastIndex(ip, ":"); idx != -1 {
				// Strip the port number if present.
				ip = ip[:idx]
			}
			
			// Get the rate limiter for the IP address, creating a new one if necessary.
			limiter := limiters.GetOrCreate(ip, func() *rate.Limiter {
				// Create a new rate limiter that allows 10 requests per minute.
				return rate.NewLimiter(rate.Every(time.Minute), 10)
			})

			// Try to allow the request.
			if !limiter.Allow() {
				// If the request is not allowed, increment the rate limit hit metric.
				metrics.RateLimitHits.WithLabelValues(ip).Inc()
				var requestID string
				if id := r.Context().Value(RequestIDKey); id != nil {
					requestID = id.(string)
				}

				// Create an error response for the rate limit exceeded error.
				errResp := errors.NewError(
					errors.RateLimitError,
					"Rate limit exceeded",
					http.StatusTooManyRequests,
					requestID,
					map[string]interface{}{
						"limit":  int64(10), // Use int64 to ensure it's not converted to float64
						"window": "1m0s",
					},
					nil,
				)

				// Write the error response to the writer.
				errors.WriteError(w, errResp)
				return
			}

			// If the request is allowed, serve the next handler.
			next.ServeHTTP(w, r)
		})
	}
}

// ResetRateLimiters resets all rate limiters. Only used for testing.
func ResetRateLimiters() {
	limiters.mu.Lock()
	defer limiters.mu.Unlock()
	limiters.visitors = make(map[string]*rate.Limiter)
}
