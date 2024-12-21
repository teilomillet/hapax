package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/teilomillet/hapax/errors"
)

// RequestTimer measures request processing time
// It wraps the HTTP handler to calculate the duration of the request
// and sets the X-Response-Time header in the response.
func RequestTimer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now() // Record the start time of the request
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor) // Wrap the response writer
		next.ServeHTTP(ww, r) // Call the next handler
		duration := time.Since(start) // Calculate the duration
		w.Header().Set("X-Response-Time", duration.String()) // Set the response header
	})
}

// PanicRecovery recovers from panics and returns a 500 error
func PanicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				errors.ErrorWithType(w, "Internal server error", errors.InternalError, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CORS handles Cross-Origin Resource Sharing
// It allows or denies requests from different origins based on the configuration.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Set CORS headers to allow cross-origin requests
        w.Header().Set("Access-Control-Allow-Origin", "*") // Allow all origins
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS") // Allow GET, POST, PUT, DELETE, and OPTIONS methods
        w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token") // Allow Accept, Authorization, Content-Type, and X-CSRF-Token headers

        // Handle preflight requests
        if r.Method == http.MethodOptions {
            // Respond with 204 No Content for preflight requests
            w.WriteHeader(http.StatusNoContent)
            return
        }

        // Call the next handler for non-preflight requests
        next.ServeHTTP(w, r)
    })
}
