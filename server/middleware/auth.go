package middleware

import (
	"net/http"

	"github.com/teilomillet/hapax/errors"
)

// Authentication middleware validates API keys and manages authentication
func Authentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			errors.ErrorWithType(w, "Missing API key", errors.AuthenticationError, http.StatusUnauthorized)
			return
		}

		// TODO: Validate API key against configuration or database
		// For now, we'll accept any non-empty key
		
		next.ServeHTTP(w, r)
	})
}
