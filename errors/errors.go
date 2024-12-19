// Package errors provides a comprehensive error handling system for the Hapax LLM gateway.
// It includes structured error types, JSON response formatting, request ID tracking,
// and integrated logging with Uber's zap logger.
//
// The package is designed to be used throughout the Hapax codebase to provide
// consistent error handling and reporting. It offers several key features:
//
//   - Structured JSON error responses with type information
//   - Request ID tracking for error correlation
//   - Integrated logging with zap
//   - Custom error types for different scenarios
//   - Middleware integration for panic recovery
//
// Basic usage:
//
//	// Simple error response
//	errors.Error(w, "Something went wrong", http.StatusBadRequest)
//
//	// Type-specific error with context
//	errors.ErrorWithType(w, "Invalid input", errors.ValidationError, http.StatusBadRequest)
//
// For more complex scenarios, you can use the error constructors in types.go:
//
//	err := errors.NewValidationError(requestID, "Invalid input", map[string]interface{}{
//	    "field": "username",
//	    "error": "required",
//	})
package errors

import (
	"encoding/json"
	"fmt"
	"net/http"

	"go.uber.org/zap"
)

// DefaultLogger is the default zap logger instance used throughout the package.
// It is initialized to a production configuration but can be overridden using SetLogger.
var DefaultLogger *zap.Logger

func init() {
	var err error
	DefaultLogger, err = zap.NewProduction()
	if err != nil {
		DefaultLogger = zap.NewNop()
	}
}

// SetLogger allows setting a custom zap logger instance.
// If nil is provided, the function will do nothing to prevent
// accidentally disabling logging.
func SetLogger(logger *zap.Logger) {
	if logger != nil {
		DefaultLogger = logger
	}
}

// ErrorType represents different categories of errors that can occur
// in the Hapax system. Each type corresponds to a specific kind of
// error scenario and carries appropriate HTTP status codes and handling logic.
type ErrorType string

const (
	// AuthError represents authentication and authorization failures
	AuthError ErrorType = "authentication_error"

	// ValidationError represents input validation failures
	ValidationError ErrorType = "validation_error"

	// InternalError represents unexpected internal server errors
	InternalError ErrorType = "internal_error"

	// ConfigError represents configuration-related errors
	ConfigError ErrorType = "config_error"

	// ProviderError represents errors from LLM providers
	ProviderError ErrorType = "provider_error"

	// RateLimitError represents rate limiting errors
	RateLimitError ErrorType = "rate_limit_error"

	// AuthenticationError represents API key authentication failures
	AuthenticationError ErrorType = "api_key_error"

	// BadRequestError represents invalid request format or parameters
	BadRequestError ErrorType = "bad_request"

	// NotFoundError represents resource not found errors
	NotFoundError ErrorType = "not_found"

	// UnauthorizedError represents unauthorized access attempts
	UnauthorizedError ErrorType = "unauthorized"
)

// HapaxError is our custom error type that implements the error interface
// and provides additional context about the error. It is designed to be
// serialized to JSON for API responses while maintaining internal error
// context for logging and debugging.
type HapaxError struct {
	// Type categorizes the error for client handling
	Type ErrorType `json:"type"`

	// Message is a human-readable error description
	Message string `json:"message"`

	// Code is the HTTP status code (not exposed in JSON)
	Code int `json:"-"`

	// RequestID links the error to a specific request
	RequestID string `json:"request_id"`

	// Details contains additional error context
	Details map[string]interface{} `json:"details,omitempty"`

	// err is the underlying error (not exposed in JSON)
	err error
}

// Error implements the error interface. It returns a string that
// combines the error type, message, and underlying error (if any).
func (e *HapaxError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Type, e.Message, e.err)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// Unwrap returns the underlying error, implementing the unwrap
// interface for error chains.
func (e *HapaxError) Unwrap() error {
	return e.err
}

// Is implements error matching for errors.Is, allowing type-based
// error matching while ignoring other fields.
func (e *HapaxError) Is(target error) bool {
	t, ok := target.(*HapaxError)
	if !ok {
		return false
	}
	return e.Type == t.Type
}

// WriteError formats and writes a HapaxError to an http.ResponseWriter.
// It sets the appropriate content type and status code, then writes
// the error as a JSON response.
func WriteError(w http.ResponseWriter, err *HapaxError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.Code)
	json.NewEncoder(w).Encode(err)
}

// Error is a drop-in replacement for http.Error that creates and writes
// a HapaxError with the InternalError type. It automatically includes
// the request ID from the response headers if available.
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

// ErrorWithType is like Error but allows specifying the error type.
// This is useful when you want to indicate specific error categories
// to the client while maintaining the simple interface of http.Error.
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
