package errors

import (
	"net/http"
	"runtime/debug"

	"go.uber.org/zap"
)

// ErrorHandler wraps an http.Handler and provides error handling
func ErrorHandler(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					stack := debug.Stack()
					logger.Error("panic recovered",
						zap.Any("error", err),
						zap.ByteString("stacktrace", stack),
						zap.String("request_id", r.Header.Get("X-Request-ID")),
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
func LogError(logger *zap.Logger, err error, requestID string) {
	if hapaxErr, ok := err.(*HapaxError); ok {
		logger.Error("request error",
			zap.String("error_type", string(hapaxErr.Type)),
			zap.String("message", hapaxErr.Message),
			zap.Int("code", hapaxErr.Code),
			zap.String("request_id", requestID),
			zap.Any("details", hapaxErr.Details),
		)
	} else {
		logger.Error("unexpected error",
			zap.Error(err),
			zap.String("request_id", requestID),
		)
	}
}
