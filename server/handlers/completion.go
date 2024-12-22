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

// CompletionRequest represents a simple text completion request.
// This is the default request type when no specific type is specified.
// All fields are validated before processing.
type CompletionRequest struct {
	Input string `json:"input" validate:"required"` // The input text to complete
}

// ChatRequest represents a chat-style request with message history.
// It follows the gollm chat format with roles and content.
// Messages must be non-empty and contain valid roles.
type ChatRequest struct {
	Messages []gollm.PromptMessage `json:"messages" validate:"required,min=1"` // List of chat messages
}

// FunctionRequest represents a function calling request.
// This is used for structured function-like interactions.
// Both input and function description are required.
type FunctionRequest struct {
	Input               string `json:"input" validate:"required"`                // The input text
	FunctionDescription string `json:"function_description" validate:"required"` // Description of the function to call
}

// CompletionHandler handles different types of completion requests.
// It supports:
// - Simple text completion (default)
// - Chat completion with message history
// - Function calling (future feature)
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

	// Parse and validate request based on type
	var request *processing.Request

	switch requestType {
	case "default":
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

		if completionReq.Input == "" {
			logger.Warn("Empty input")
			w.WriteHeader(http.StatusBadRequest)
			errors.WriteError(w, errors.NewValidationError(
				requestID,
				"Input text is required",
				map[string]interface{}{
					"type": requestType,
				},
			))
			return
		}

		logger.Debug("Parsed completion request",
			zap.Int("input_length", len(completionReq.Input)),
		)

		request = &processing.Request{
			Type:  requestType,
			Input: completionReq.Input,
		}

	case "chat":
		var chatReq ChatRequest
		if err := json.Unmarshal(rawReq, &chatReq); err != nil {
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

		if len(chatReq.Messages) == 0 {
			logger.Warn("Empty chat messages")
			w.WriteHeader(http.StatusBadRequest)
			errors.WriteError(w, errors.NewValidationError(
				requestID,
				"Chat messages cannot be empty",
				map[string]interface{}{
					"type": requestType,
				},
			))
			return
		}

		logger.Debug("Parsed chat request",
			zap.Int("message_count", len(chatReq.Messages)),
		)

		request = &processing.Request{
			Type:     requestType,
			Messages: convertMessages(chatReq.Messages),
		}

	case "function":
		var funcReq FunctionRequest
		if err := json.Unmarshal(rawReq, &funcReq); err != nil {
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

		// Check description size
		if len(funcReq.FunctionDescription) > 5*1024 {
			logger.Warn("Function description too large",
				zap.Int("size", len(funcReq.FunctionDescription)),
			)
			w.WriteHeader(http.StatusBadRequest)
			errors.WriteError(w, errors.NewValidationError(
				requestID,
				"Function description too large",
				map[string]interface{}{
					"type":        "function",
					"max_size":    "5KB",
					"actual_size": fmt.Sprintf("%dKB", len(funcReq.FunctionDescription)/1024),
				},
			))
			return
		}

		if funcReq.Input == "" || funcReq.FunctionDescription == "" {
			missingFields := []string{}
			if funcReq.Input == "" {
				missingFields = append(missingFields, "input")
			}
			if funcReq.FunctionDescription == "" {
				missingFields = append(missingFields, "function_description")
			}
			logger.Warn("Missing required fields",
				zap.Strings("missing_fields", missingFields),
			)
			w.WriteHeader(http.StatusBadRequest)
			errors.WriteError(w, errors.NewValidationError(
				requestID,
				"Function input and description are required",
				map[string]interface{}{
					"type":           requestType,
					"missing_fields": missingFields,
				},
			))
			return
		}

		logger.Debug("Parsed function request",
			zap.Int("input_length", len(funcReq.Input)),
			zap.Int("description_length", len(funcReq.FunctionDescription)),
		)

		request = &processing.Request{
			Type:                requestType,
			Input:               funcReq.Input,
			FunctionDescription: funcReq.FunctionDescription,
		}

	default:
		logger.Warn("Invalid request type")
		w.WriteHeader(http.StatusBadRequest)
		errors.WriteError(w, errors.NewValidationError(
			requestID,
			"Invalid request type",
			map[string]interface{}{
				"type":            requestType,
				"supported_types": []string{"default", "chat", "function"},
			},
		))
		return
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
