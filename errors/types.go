package errors

import (
	"net/http"
)

// NewError creates a new HapaxError with the given parameters.
// It is a general-purpose constructor that allows full control over
// the error's fields. For most cases, you should use one of the
// specialized constructors below.
//
// Example:
//
//	err := NewError(InternalError, "database connection failed", 500, "req_123", nil, dbErr)
func NewError(errType ErrorType, message string, code int, requestID string, details map[string]interface{}, err error) *HapaxError {
	return &HapaxError{
		Type:      errType,
		Message:   message,
		Code:      code,
		RequestID: requestID,
		Details:   details,
		err:       err,
	}
}

// NewAuthError creates an authentication error with appropriate defaults.
// Use this for any authentication or authorization failures, such as:
//   - Invalid API keys
//   - Missing credentials
//   - Insufficient permissions
//
// Example:
//
//	err := NewAuthError("req_123", "Invalid API key", nil)
func NewAuthError(requestID, message string, err error) *HapaxError {
	return &HapaxError{
		Type:      AuthError,
		Message:   message,
		Code:      http.StatusUnauthorized,
		RequestID: requestID,
		err:       err,
		Details: map[string]interface{}{
			"suggestion": "Please check your authentication credentials",
		},
	}
}

// NewValidationError creates a validation error with appropriate defaults.
// Use this for any request validation failures, such as:
//   - Invalid input formats
//   - Missing required fields
//   - Value constraint violations
//
// Example:
//
//	err := NewValidationError("req_123", "Invalid prompt", map[string]interface{}{
//	    "field": "prompt",
//	    "error": "must not be empty",
//	})
func NewValidationError(requestID, message string, validationDetails map[string]interface{}) *HapaxError {
	return &HapaxError{
		Type:      ValidationError,
		Message:   message,
		Code:      http.StatusBadRequest,
		RequestID: requestID,
		Details:   validationDetails,
	}
}

// NewRateLimitError creates a rate limit error with appropriate defaults.
// Use this when a client has exceeded their quota or rate limits, such as:
//   - Too many requests per second
//   - Monthly API quota exceeded
//   - Concurrent request limit reached
//
// Example:
//
//	err := NewRateLimitError("req_123", 30)
func NewRateLimitError(requestID string, retryAfter int) *HapaxError {
	return &HapaxError{
		Type:      RateLimitError,
		Message:   "Rate limit exceeded",
		Code:      http.StatusTooManyRequests,
		RequestID: requestID,
		Details: map[string]interface{}{
			"retry_after": retryAfter,
		},
	}
}

// NewProviderError creates a provider error with appropriate defaults.
// Use this when the underlying LLM provider encounters an error, such as:
//   - Provider API errors
//   - Model unavailability
//   - Invalid provider configuration
//
// Example:
//
//	err := NewProviderError("req_123", "Model unavailable", providerErr)
func NewProviderError(requestID string, message string, err error) *HapaxError {
	return &HapaxError{
		Type:      ProviderError,
		Message:   message,
		Code:      http.StatusBadGateway,
		RequestID: requestID,
		err:       err,
	}
}

// NewInternalError creates an internal server error with appropriate defaults.
// Use this for unexpected errors that are not covered by other error types:
//   - Panics
//   - Database errors
//   - Unexpected system failures
//
// Example:
//
//	err := NewInternalError("req_123", dbErr)
func NewInternalError(requestID string, err error) *HapaxError {
	return &HapaxError{
		Type:      InternalError,
		Message:   "An internal error occurred",
		Code:      http.StatusInternalServerError,
		RequestID: requestID,
		err:       err,
	}
}
