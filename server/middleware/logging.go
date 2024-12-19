package middleware

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// ResponseWriter wraps http.ResponseWriter to capture status code and size
type ResponseWriter struct {
	http.ResponseWriter
	status int
	size   int64
}

// NewResponseWriter creates a new ResponseWriter
func NewResponseWriter(w http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{ResponseWriter: w}
}

func (w *ResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *ResponseWriter) Write(b []byte) (int, error) {
	size, err := w.ResponseWriter.Write(b)
	w.size += int64(size)
	return size, err
}

// Status returns the status code
func (w *ResponseWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// Size returns the response size
func (w *ResponseWriter) Size() int64 {
	return w.size
}

// Logging middleware logs request and response details
func Logging(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := NewResponseWriter(w)

			// Log request details
			logger.Info("Request started",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("user_agent", r.UserAgent()),
			)

			next.ServeHTTP(rw, r)

			// Log response details
			logger.Info("Request completed",
				zap.Duration("duration", time.Since(start)),
				zap.Int("status", rw.Status()),
				zap.Int64("size", rw.Size()),
			)
		})
	}
}
