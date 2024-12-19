package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/teilomillet/hapax/errors"
	"golang.org/x/time/rate"
)

var (
	visitors = make(map[string]*rate.Limiter)
	mu       sync.RWMutex
)

// getVisitor retrieves or creates a rate limiter for an IP
func getVisitor(ip string) *rate.Limiter {
	mu.Lock()
	defer mu.Unlock()

	limiter, exists := visitors[ip]
	if !exists {
		// Create a new rate limiter allowing 10 requests per second with a burst of 20
		limiter = rate.NewLimiter(rate.Every(time.Second/10), 20)
		visitors[ip] = limiter
	}

	return limiter
}

// RateLimit middleware implements a token bucket algorithm for rate limiting
func RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get IP address from request
		ip := r.RemoteAddr
		
		// Get rate limiter for this IP
		limiter := getVisitor(ip)
		
		if !limiter.Allow() {
			errors.ErrorWithType(w, "Rate limit exceeded", errors.RateLimitError, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
