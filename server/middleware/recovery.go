// Package middleware provides various middleware functions for HTTP handlers.
package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/teilomillet/hapax/errors"
	"go.uber.org/zap"
)

// Recovery middleware recovers from panics and logs the error
// It takes a zap.Logger instance for logging errors.
func Recovery(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Defer a function to recover from panics
			defer func() {
				if err := recover(); err != nil {
					// Capture the stack trace
					stack := debug.Stack()
					// Log the error and stack trace
					logger.Error("Panic recovered",
						zap.Any("error", err),
						zap.ByteString("stack", stack),
					)
					
					// Retrieve the request ID from the context
					requestID := r.Context().Value("request_id").(string)
					// Write an internal server error response
					errors.WriteError(w, errors.NewInternalError(
						requestID,
						fmt.Errorf("internal server error: %v", err),
					))
				}
			}()

			// Call the next handler in the chain
			next.ServeHTTP(w, r)
		})
	}
}
