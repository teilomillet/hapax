package middleware

import (
	"net/http"
	"strings"

	"github.com/teilomillet/hapax/errors"
)

// Authentication middleware validates API keys and manages authentication
func Authentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for API key
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "" {
			// TODO: Validate API key against configuration or database
			// For now, we'll accept any non-empty key
			next.ServeHTTP(w, r)
			return
		}

		// Check for Bearer token
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token != "" {
				// TODO: Validate token against configuration or database
				// For now, we'll accept any non-empty token
				next.ServeHTTP(w, r)
				return
			}
		}

		errors.ErrorWithType(w, "Missing or invalid authentication", errors.AuthenticationError, http.StatusUnauthorized)
	})
}
