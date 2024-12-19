package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/teilomillet/hapax/errors"
)

const defaultTimeout = 5 * time.Second

// timeoutWriter wraps http.ResponseWriter to track if a response has been written
type timeoutWriter struct {
	http.ResponseWriter
	written chan bool
}

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

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.ResponseWriter.WriteHeader(code)
	select {
	case tw.written <- true:
	default:
	}
}

func (tw *timeoutWriter) hasWritten() bool {
	select {
	case <-tw.written:
		return true
	default:
		return false
	}
}

// Timeout middleware adds a timeout to the request context
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Create a context with timeout
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			// Create a channel to signal completion
			done := make(chan struct{})
			
			// Create a response writer wrapper
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
					if id := r.Context().Value("request_id"); id != nil {
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
