package errors

import (
	"encoding/json"
	"fmt"
	"net/http"

	"go.uber.org/zap"
)

// DefaultLogger is the default zap logger instance
var DefaultLogger *zap.Logger

func init() {
	var err error
	DefaultLogger, err = zap.NewProduction()
	if err != nil {
		DefaultLogger = zap.NewNop()
	}
}

// SetLogger allows setting a custom logger
func SetLogger(logger *zap.Logger) {
	if logger != nil {
		DefaultLogger = logger
	}
}

// ErrorType represents different categories of errors
type ErrorType string

const (
	AuthError       ErrorType = "authentication_error"
	ValidationError ErrorType = "validation_error"
	RateLimitError  ErrorType = "rate_limit_error"
	ProviderError   ErrorType = "provider_error"
	InternalError   ErrorType = "internal_error"
)

// HapaxError is our custom error type that carries additional context
type HapaxError struct {
	Type      ErrorType              `json:"type"`
	Message   string                 `json:"message"`
	Code      int                    `json:"-"`
	RequestID string                 `json:"request_id"`
	Details   map[string]interface{} `json:"details,omitempty"`
	err       error
}

// Error implements the error interface
func (e *HapaxError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Type, e.Message, e.err)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// Unwrap returns the underlying error
func (e *HapaxError) Unwrap() error {
	return e.err
}

// Is implements error matching for errors.Is
func (e *HapaxError) Is(target error) bool {
	t, ok := target.(*HapaxError)
	if !ok {
		return false
	}
	return e.Type == t.Type
}

// WriteError writes the error to the http.ResponseWriter
func WriteError(w http.ResponseWriter, err *HapaxError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.Code)
	json.NewEncoder(w).Encode(err)
}

// Error is a drop-in replacement for http.Error
func Error(w http.ResponseWriter, message string, code int) {
	requestID := w.Header().Get("X-Request-ID")
	err := &HapaxError{
		Type:      InternalError,
		Message:   message,
		Code:      code,
		RequestID: requestID,
	}
	WriteError(w, err)
}

// ErrorWithType is like Error but allows specifying the error type
func ErrorWithType(w http.ResponseWriter, message string, errType ErrorType, code int) {
	requestID := w.Header().Get("X-Request-ID")
	err := &HapaxError{
		Type:      errType,
		Message:   message,
		Code:      code,
		RequestID: requestID,
	}
	WriteError(w, err)
}
