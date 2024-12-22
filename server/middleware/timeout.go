// Package middleware provides various middleware functions for HTTP handlers.
package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/teilomillet/hapax/errors"
)

const defaultTimeout = 5 * time.Second

// timeoutWriter wraps http.ResponseWriter to track if a response has been written
// It uses a channel to signal when the response has been sent.
type timeoutWriter struct {
	http.ResponseWriter
	written chan bool
}

// Write writes the data to the connection and tracks if the response has been written.
func (tw *timeoutWriter) Write(b []byte) (int, error) {
	n, err := tw.ResponseWriter.Write(b)
	if n > 0 {
		select {
		case tw.written <- true:
		default:
		}
	}
	return n, err
}

// WriteHeader sends an HTTP response header and tracks if the response has been written.
func (tw *timeoutWriter) WriteHeader(code int) {
	// Call the original WriteHeader method.
	tw.ResponseWriter.WriteHeader(code)
	select {
	case tw.written <- true:
	default:
	}
}

// hasWritten checks if the response has been written.
func (tw *timeoutWriter) hasWritten() bool {
	select {
	case <-tw.written:
		return true
	default:
		return false
	}
}

// Timeout middleware adds a timeout to the request context
// It allows you to specify a duration after which the request will be aborted if not completed.
// 
// The Timeout middleware works by creating a new context with a timeout, and using a custom 
// timeoutWriter to track whether a response has been written. If the request times out and 
// no response has been written, it sends a timeout error response.
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Create a context with timeout
			if timeout == 0 {
				timeout = defaultTimeout
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel() // Ensure cancel is called to release resources
			
			// Create a channel to signal completion
			done := make(chan struct{})
			
			// Use the custom timeoutWriter to track response status.
			tw := &timeoutWriter{
				ResponseWriter: w,
				written:       make(chan bool, 1),
			}

			// Process the request in a goroutine
			go func() {
				defer func() {
					close(done)
					if ctx.Err() == context.Canceled {
						cancel()
					}
				}()
				next.ServeHTTP(tw, r.WithContext(ctx))
			}()

			// Wait for either completion or timeout
			select {
			case <-done:
				// Request completed normally
				return
			case <-ctx.Done():
				// Request timed out
				if !tw.hasWritten() {
					// Only write error if nothing has been written yet
					var requestID string
					if id := r.Context().Value(RequestIDKey); id != nil {
						requestID = id.(string)
					}

					errResp := errors.NewError(
						errors.InternalError,
						"Request timeout",
						http.StatusGatewayTimeout,
						requestID,
						map[string]interface{}{
							"timeout": timeout.String(),
						},
						ctx.Err(),
					)

					errors.WriteError(tw, errResp)
				}
				// Cancel the context to stop the goroutine
				cancel()
				return
			}
		})
	}
}
