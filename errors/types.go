package errors

import "net/http"

// NewAuthError creates a new authentication error
func NewAuthError(requestID string, message string, err error) *HapaxError {
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

// NewValidationError creates a new validation error
func NewValidationError(requestID string, message string, validationDetails map[string]interface{}) *HapaxError {
	return &HapaxError{
		Type:      ValidationError,
		Message:   message,
		Code:      http.StatusBadRequest,
		RequestID: requestID,
		Details:   validationDetails,
	}
}

// NewRateLimitError creates a new rate limit error
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

// NewProviderError creates a new provider-related error
func NewProviderError(requestID string, message string, err error) *HapaxError {
	return &HapaxError{
		Type:      ProviderError,
		Message:   message,
		Code:      http.StatusBadGateway,
		RequestID: requestID,
		err:       err,
	}
}

// NewInternalError creates a new internal server error
func NewInternalError(requestID string, err error) *HapaxError {
	return &HapaxError{
		Type:      InternalError,
		Message:   "An internal error occurred",
		Code:      http.StatusInternalServerError,
		RequestID: requestID,
		err:       err,
	}
}
