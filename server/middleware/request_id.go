// Package middleware provides various middleware functions for HTTP handlers.
package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type contextKey string

// RequestIDKey is the key used to store the request ID in the context.
const RequestIDKey contextKey = "request_id"

// RequestID middleware adds a unique request ID to the context
// and sets it in the response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate a unique request ID using UUID.
		requestID := uuid.New().String()

		// Set the request ID in the response header for tracking.
		w.Header().Set("X-Request-ID", requestID)

		// Add the request ID to the request context for downstream handlers.
		ctx := context.WithValue(r.Context(), RequestIDKey, requestID)
		// Call the next handler with the updated context.
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
