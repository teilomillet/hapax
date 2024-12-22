// Package errors provides error response utilities.
package errors

import (
	"errors"
)

const RequestIDKey = "request_id"

// ErrorResponse represents a standardized error response format
// that is returned to clients when an error occurs. It includes:
//   - Error type for categorization
//   - Human-readable message
//   - Request ID for correlation
//   - Optional details for additional context
type ErrorResponse struct {
	Type      ErrorType              `json:"type"`
	Message   string                 `json:"message"`
	RequestID string                 `json:"request_id"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// As is a wrapper around errors.As for better error type assertion
func As(err error, target interface{}) bool {
	return errors.As(err, target)
}
