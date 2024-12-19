package errors

import (
	"errors"
	"net/http"
	"testing"
)

func TestNewAuthError(t *testing.T) {
	requestID := "test-123"
	message := "invalid credentials"
	innerErr := errors.New("token expired")

	err := NewAuthError(requestID, message, innerErr)

	if err.Type != AuthError {
		t.Errorf("Expected error type %v, got %v", AuthError, err.Type)
	}
	if err.Message != message {
		t.Errorf("Expected message %v, got %v", message, err.Message)
	}
	if err.Code != http.StatusUnauthorized {
		t.Errorf("Expected code %v, got %v", http.StatusUnauthorized, err.Code)
	}
	if err.RequestID != requestID {
		t.Errorf("Expected requestID %v, got %v", requestID, err.RequestID)
	}
	if err.Unwrap() != innerErr {
		t.Errorf("Expected inner error %v, got %v", innerErr, err.Unwrap())
	}
}

func TestNewValidationError(t *testing.T) {
	requestID := "test-456"
	message := "invalid input"
	details := map[string]interface{}{
		"field": "email",
		"error": "invalid format",
	}

	err := NewValidationError(requestID, message, details)

	if err.Type != ValidationError {
		t.Errorf("Expected error type %v, got %v", ValidationError, err.Type)
	}
	if err.Message != message {
		t.Errorf("Expected message %v, got %v", message, err.Message)
	}
	if err.Code != http.StatusBadRequest {
		t.Errorf("Expected code %v, got %v", http.StatusBadRequest, err.Code)
	}
	if err.RequestID != requestID {
		t.Errorf("Expected requestID %v, got %v", requestID, err.RequestID)
	}
	if err.Details["field"] != details["field"] {
		t.Errorf("Expected details field %v, got %v", details["field"], err.Details["field"])
	}
}

func TestNewRateLimitError(t *testing.T) {
	requestID := "test-789"
	retryAfter := 60

	err := NewRateLimitError(requestID, retryAfter)

	if err.Type != RateLimitError {
		t.Errorf("Expected error type %v, got %v", RateLimitError, err.Type)
	}
	if err.Code != http.StatusTooManyRequests {
		t.Errorf("Expected code %v, got %v", http.StatusTooManyRequests, err.Code)
	}
	if err.Details["retry_after"] != retryAfter {
		t.Errorf("Expected retry_after %v, got %v", retryAfter, err.Details["retry_after"])
	}
}
