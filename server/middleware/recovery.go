package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/teilomillet/hapax/errors"
	"go.uber.org/zap"
)

// Recovery middleware recovers from panics and logs the error
func Recovery(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					stack := debug.Stack()
					logger.Error("Panic recovered",
						zap.Any("error", err),
						zap.ByteString("stack", stack),
					)

					requestID := r.Context().Value("request_id").(string)
					errors.WriteError(w, errors.NewInternalError(
						requestID,
						fmt.Errorf("internal server error: %v", err),
					))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
