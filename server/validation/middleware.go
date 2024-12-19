package validation

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"

	"github.com/go-playground/validator/v10"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/errors"
)

var (
	validate = validator.New()
	counter  *TokenCounter
	cfg     *config.Config
)

func init() {
	// Initialize with a default model, can be overridden
	var err error
	counter, err = NewTokenCounter("gpt-4")
	if err != nil {
		panic(fmt.Sprintf("failed to initialize token counter: %v", err))
	}
}

// Initialize initializes the validation middleware with configuration
func Initialize(c *config.Config) error {
	cfg = c
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		return fld.Tag.Get("json")
	})

	var err error
	counter, err = NewTokenCounter(cfg.LLM.Model)
	if err != nil {
		return fmt.Errorf("failed to initialize token counter: %v", err)
	}
	return nil
}

// ValidateCompletion validates completion request bodies
func ValidateCompletion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil {
			errors.ErrorWithType(w, "Validation middleware not initialized", errors.InternalError, http.StatusInternalServerError)
			return
		}

		// Check Content-Type
		if ct := r.Header.Get("Content-Type"); ct == "" {
			errors.ErrorWithType(w, "Content-Type header is required", errors.ValidationError, http.StatusBadRequest)
			return
		} else if ct != "application/json" {
			errors.ErrorWithType(w, "Content-Type must be application/json", errors.ValidationError, http.StatusBadRequest)
			return
		}

		// Read body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			errors.ErrorWithType(w, "Failed to read request body", errors.InternalError, http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		// Parse request
		var req CompletionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			errors.ErrorWithType(w, "Invalid JSON format", errors.ValidationError, http.StatusBadRequest)
			return
		}

		// Validate schema
		if err := validate.Struct(req); err != nil {
			var validationErrors []string
			for _, err := range err.(validator.ValidationErrors) {
				validationErrors = append(validationErrors, formatValidationError(err))
			}
			details := map[string]interface{}{
				"validation_errors": validationErrors,
			}
			err := &errors.HapaxError{
				Type:    errors.ValidationError,
				Message: "Request validation failed",
				Code:    http.StatusBadRequest,
				Details: details,
			}
			errors.WriteError(w, err)
			return
		}

		// Validate request options
		if err := ValidateOptions(req.Options); err != nil {
			errors.ErrorWithType(w, err.Error(), errors.ValidationError, http.StatusBadRequest)
			return
		}

		// Validate tokens
		if err := counter.ValidateTokens(req, cfg.LLM.MaxContextTokens); err != nil {
			errors.ErrorWithType(w, err.Error(), errors.ValidationError, http.StatusBadRequest)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// formatValidationError converts a validator.FieldError into a human-readable string
func formatValidationError(err validator.FieldError) string {
	switch err.Tag() {
	case "required":
		return fmt.Sprintf("Field '%s' is required", err.Field())
	case "oneof":
		return fmt.Sprintf("Field '%s' must be one of [%s]", err.Field(), err.Param())
	case "gte", "lte":
		return fmt.Sprintf("Field '%s' must be between %s", err.Field(), err.Param())
	default:
		return fmt.Sprintf("Field '%s' failed validation: %s", err.Field(), err.Tag())
	}
}
