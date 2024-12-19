package errors

import (
	"errors"
	"testing"
)

func TestHapaxError_Error(t *testing.T) {
	tests := []struct {
		name    string
		err     *HapaxError
		want    string
		wantErr bool
	}{
		{
			name: "basic error without wrapped error",
			err: &HapaxError{
				Type:    ValidationError,
				Message: "invalid input",
			},
			want: "validation_error: invalid input",
		},
		{
			name: "error with wrapped error",
			err: &HapaxError{
				Type:    InternalError,
				Message: "processing failed",
				err:     errors.New("database connection failed"),
			},
			want: "internal_error: processing failed: database connection failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.want {
				t.Errorf("HapaxError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHapaxError_Is(t *testing.T) {
	err1 := &HapaxError{Type: AuthError, Message: "test1"}
	err2 := &HapaxError{Type: AuthError, Message: "test2"}
	err3 := &HapaxError{Type: ValidationError, Message: "test3"}

	if !err1.Is(err2) {
		t.Error("Expected err1.Is(err2) to be true for same error type")
	}

	if err1.Is(err3) {
		t.Error("Expected err1.Is(err3) to be false for different error types")
	}
}

func TestHapaxError_Unwrap(t *testing.T) {
	innerErr := errors.New("inner error")
	err := &HapaxError{
		Type:    InternalError,
		Message: "outer error",
		err:     innerErr,
	}

	if unwrapped := err.Unwrap(); unwrapped != innerErr {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, innerErr)
	}
}
