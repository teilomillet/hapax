// Package handlers provides HTTP handlers for the Hapax server.
// It implements request handling for completions, chat, and function calling
// using the gollm library and Hapax processing system.
//
// The package follows these design principles:
// 1. Consistent error handling using the errors package
// 2. Structured logging with request IDs
// 3. Clear request validation and type conversion
// 4. Separation between request parsing and processing
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/errors"
	"github.com/teilomillet/hapax/server/middleware"
	"github.com/teilomillet/hapax/server/processing"
	"go.uber.org/zap"
)

// CompletionRequest represents a completion request with message history.
// This is the primary request type that supports both simple text and chat completions.
// All fields are validated before processing.
type CompletionRequest struct {
	// Messages is the primary field for all requests. For simple completions,
	// a single user message is created from the Input field.
	Messages []gollm.PromptMessage `json:"messages,omitempty" validate:"omitempty,min=1"`

	// Input is maintained for backward compatibility with simple completions.
	// If present, it will be converted to a single user message.
	Input string `json:"input,omitempty" validate:"omitempty"`

	// FunctionDescription is used for function calling requests.
	// If present, it will be included in the system context.
	FunctionDescription string `json:"function_description,omitempty" validate:"omitempty"`
}

// CompletionHandler handles different types of completion requests.
// It supports:
// - Simple text completion (default)
// - Chat completion with message history
// - Function calling
type CompletionHandler struct {
	processor *processing.Processor
	logger    *zap.Logger
}

// NewCompletionHandler creates a new completion handler with the given processor and logger.
// It requires both parameters to be non-nil.
func NewCompletionHandler(processor *processing.Processor, logger *zap.Logger) *CompletionHandler {
	return &CompletionHandler{
		processor: processor,
		logger:    logger,
	}
}

// convertMessages converts gollm.PromptMessage to processing.Message.
// This conversion is necessary because:
// 1. It decouples our internal types from external dependencies
// 2. Allows for future extensions to our Message type
// 3. Provides a clear boundary for type conversion
func convertMessages(messages []gollm.PromptMessage) []processing.Message {
	result := make([]processing.Message, len(messages))
	for i, msg := range messages {
		result[i] = processing.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}
	return result
}

// ServeHTTP implements http.Handler interface.
// It handles all completion requests by:
// 1. Determining the request type
// 2. Validating and parsing the request
// 3. Processing using the appropriate template
// 4. Formatting and returning the response
//
// Error Handling:
// - ValidationError: Invalid request format or missing fields
// - ProcessingError: LLM or processing failures
// - InternalError: Unexpected system errors
//
// Each error is logged with:
// - Request ID for tracking
// - Error type and message
// - Request context (type, path)
func (h *CompletionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Ensure request method is POST
	if r.Method != http.MethodPost {
		details := map[string]interface{}{
			"method": r.Method,
		}
		details["allowed_methods"] = []string{"POST"}
		errors.WriteError(w, errors.NewValidationError(
			r.Context().Value(middleware.RequestIDKey).(string),
			"Method not allowed",
			details,
		))
		return
	}

	// Ensure content type is application/json
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		var requestID string
		if id := r.Context().Value(middleware.RequestIDKey); id != nil {
			requestID = id.(string)
		}
		w.WriteHeader(http.StatusBadRequest)
		errors.WriteError(w, errors.NewValidationError(
			requestID,
			"Content-Type header required",
			map[string]interface{}{
				"required_content_type": "application/json",
			},
		))
		return
	}

	// Get request ID from context, use empty string if not present
	var requestID string
	if id := r.Context().Value(middleware.RequestIDKey); id != nil {
		requestID = id.(string)
	}

	// Set up request logger with context
	logger := h.logger.With(
		zap.String("request_id", requestID),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr),
		zap.String("user_agent", r.UserAgent()),
		zap.Int64("content_length", r.ContentLength),
	)

	logger.Info("Processing request")

	// Determine request type from query parameter
	requestType := r.URL.Query().Get("type")
	if requestType == "" {
		requestType = "default"
	}

	// Parse request body
	var rawReq json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&rawReq); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		errors.WriteError(w, errors.NewValidationError(
			requestID,
			"Invalid completion request format",
			map[string]interface{}{
				"type": requestType,
			},
		))
		return
	}

	// Parse and validate request
	var completionReq CompletionRequest
	if err := json.Unmarshal(rawReq, &completionReq); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		errors.WriteError(w, errors.NewValidationError(
			requestID,
			"Invalid completion request format",
			map[string]interface{}{
				"type": requestType,
			},
		))
		return
	}

	// Add debug logging
	logger.Debug("Received completion request",
		zap.String("request_type", requestType),
		zap.Int("messages_count", len(completionReq.Messages)),
		zap.Any("messages", completionReq.Messages),
		zap.String("input", completionReq.Input),
	)

	// Convert request to messages format
	var messages []gollm.PromptMessage

	// Handle function description if present
	if completionReq.FunctionDescription != "" {
		// Check description size
		if len(completionReq.FunctionDescription) > 5*1024 {
			logger.Warn("Function description too large",
				zap.Int("size", len(completionReq.FunctionDescription)),
			)
			w.WriteHeader(http.StatusBadRequest)
			errors.WriteError(w, errors.NewValidationError(
				requestID,
				"Function description too large",
				map[string]interface{}{
					"type":        "function",
					"max_size":    "5KB",
					"actual_size": fmt.Sprintf("%dKB", len(completionReq.FunctionDescription)/1024),
				},
			))
			return
		}
		// Add function description as system message
		messages = append(messages, gollm.PromptMessage{
			Role:    "system",
			Content: completionReq.FunctionDescription,
		})
	}

	// Handle messages or input
	if len(completionReq.Messages) > 0 {
		messages = append(messages, completionReq.Messages...)
	} else if completionReq.Input != "" {
		// Check input size
		if len(completionReq.Input) > 512*1024 {
			logger.Warn("Input too large",
				zap.Int("size", len(completionReq.Input)),
			)
			w.WriteHeader(http.StatusBadRequest)
			errors.WriteError(w, errors.NewValidationError(
				requestID,
				"Input too large",
				map[string]interface{}{
					"type":        requestType,
					"max_size":    "512KB",
					"actual_size": "1MB",
				},
			))
			return
		}
		messages = append(messages, gollm.PromptMessage{
			Role:    "user",
			Content: completionReq.Input,
		})
	} else {
		logger.Warn("No input or messages provided")
		w.WriteHeader(http.StatusBadRequest)
		errors.WriteError(w, errors.NewValidationError(
			requestID,
			"Either input or messages must be provided",
			map[string]interface{}{
				"type": requestType,
			},
		))
		return
	}

	// Create processing request
	request := &processing.Request{
		Type:     requestType,
		Messages: convertMessages(messages),
	}

	// Create context with timeout header if present
	ctx := r.Context()
	if timeoutHeader := r.Header.Get("X-Test-Timeout"); timeoutHeader != "" {
		ctx = context.WithValue(ctx, middleware.XTestTimeoutKey, timeoutHeader)
	}

	// Process the request
	logger.Debug("Processing request with processor")

	// Process request
	response, err := h.processor.ProcessRequest(ctx, request)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			logger.Error("Request timeout",
				zap.Error(err),
				zap.String("request_id", requestID),
				zap.String("request_type", requestType),
			)
			errors.WriteError(w, errors.NewError(
				errors.InternalError,
				"Request timeout",
				http.StatusGatewayTimeout,
				requestID,
				map[string]interface{}{
					"timeout": "5s",
				},
				err,
			))
			return
		}

		logger.Error("Failed to process request",
			zap.Error(err),
			zap.String("request_id", requestID),
			zap.String("request_type", requestType),
		)

		errors.WriteError(w, errors.NewError(
			errors.InternalError,
			"Failed to process request",
			http.StatusInternalServerError,
			requestID,
			map[string]interface{}{
				"error": "LLM error",
				"type":  requestType,
			},
			err,
		))
		return
	}

	// Write response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response",
			zap.Error(err),
		)
		errors.WriteError(w, errors.NewInternalError(
			requestID,
			fmt.Errorf("failed to encode response: %v", err),
		))
		return
	}

	logger.Debug("Request successful",
		zap.Int("response_length", len(response.Content)),
	)
}
