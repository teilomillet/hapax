// Package errors provides error handling middleware and utilities.
package errors

import (
	"net/http"
	"runtime/debug"

	"go.uber.org/zap"
)

// ErrorHandler wraps an http.Handler and provides error handling
// If a panic occurs during request processing, it:
//  1. Logs the panic and stack trace
//  2. Returns a 500 Internal Server Error to the client
//  3. Includes the request ID in both the log and response
//
// The panic recovery ensures that the server continues running even if
// individual requests panic. All panics are logged with their stack traces
// for debugging purposes.
//
// Example usage:
//
//	router.Use(errors.ErrorHandler(logger))
func ErrorHandler(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					stack := debug.Stack()
					logger.Error("panic recovered",
						zap.Any("error", err),
						zap.ByteString("stacktrace", stack),
						zap.String(string(RequestIDKey), r.Header.Get("X-Request-ID")),
					)

					hapaxErr := NewInternalError(r.Header.Get("X-Request-ID"), nil)
					WriteError(w, hapaxErr)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// LogError logs an error with its context
// It ensures that all errors are properly logged with their context, including:
//   - Error type and message
//   - Request ID
//   - HTTP method and URL
//   - Status code
//
// Example usage:
//
//	errors.LogError(logger, err, requestID)
func LogError(logger *zap.Logger, err error, requestID string) {
	if hapaxErr, ok := err.(*HapaxError); ok {
		logger.Error("request error",
			zap.String("error_type", string(hapaxErr.Type)),
			zap.String("message", hapaxErr.Message),
			zap.Int("code", hapaxErr.Code),
			zap.String(string(RequestIDKey), requestID),
			zap.Any("details", hapaxErr.Details),
		)
	} else {
		logger.Error("unexpected error",
			zap.Error(err),
			zap.String(string(RequestIDKey), requestID),
		)
	}
}
