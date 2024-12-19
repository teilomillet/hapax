package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/teilomillet/hapax/errors"
	"golang.org/x/time/rate"
)

type rateLimiters struct {
	visitors map[string]*rate.Limiter
	mu      sync.RWMutex
}

var (
	limiters = &rateLimiters{
		visitors: make(map[string]*rate.Limiter),
	}
)

func (l *rateLimiters) GetOrCreate(ip string, create func() *rate.Limiter) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	limiter, exists := l.visitors[ip]
	if !exists {
		limiter = create()
		l.visitors[ip] = limiter
	}

	return limiter
}

// RateLimit middleware implements rate limiting per IP address
func RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get IP address from request
		ip := r.RemoteAddr
		if idx := strings.LastIndex(ip, ":"); idx != -1 {
			ip = ip[:idx] // Strip port number if present
		}
		
		// Get rate limiter for this IP
		limiter := limiters.GetOrCreate(ip, func() *rate.Limiter {
			return rate.NewLimiter(rate.Every(time.Minute), 10)
		})

		// Try to allow request
		if !limiter.Allow() {
			var requestID string
			if id := r.Context().Value("request_id"); id != nil {
				requestID = id.(string)
			}

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

			errors.WriteError(w, errResp)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ResetRateLimiters resets all rate limiters. Only used for testing.
func ResetRateLimiters() {
	limiters.mu.Lock()
	defer limiters.mu.Unlock()
	limiters.visitors = make(map[string]*rate.Limiter)
}
