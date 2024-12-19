// Package processing provides request processing and response formatting for LLM interactions.
// It handles template-based request transformation, LLM communication, and response formatting.
package processing

// Message represents a single message in a conversation.
// This follows the standard chat format used by most LLM providers,
// where each message has a role (e.g., "user", "assistant", "system")
// and content (the actual message text).
type Message struct {
	Role    string `json:"role"`    // Role of the message sender (e.g., "user", "assistant")
	Content string `json:"content"` // The actual message content
}

// Request represents an incoming request to the LLM service.
// It supports two main types of requests:
// 1. Simple completion: Using the Input field with a default template
// 2. Chat completion: Using the Messages field with a chat template
//
// The Type field determines which template is used to format the request.
// This allows for flexible request handling while maintaining a consistent
// interface with the LLM.
type Request struct {
	// Type indicates the type of request (e.g., "completion", "chat", "function")
	Type     string    `json:"type"`              // Type of request (e.g., "default", "chat")
	Input    string    `json:"input"`             // Used for simple completion requests
	Messages []Message `json:"messages,omitempty"` // Used for chat completion requests
	// FunctionDescription is used for function-calling requests
	FunctionDescription string `json:"function_description,omitempty"`
}

// Response represents the processed output from the LLM.
// It contains the formatted content after applying any configured
// transformations (e.g., JSON cleaning, whitespace trimming, length limits).
//
// Future extensions might include:
// - Metadata about the processing (e.g., truncation info)
// - Multiple response formats (e.g., text, structured data)
// - Usage statistics (tokens, processing time)
type Response struct {
	// Content is the processed response content
	Content string `json:"content"` // The processed response content
	// Error holds any error information
	Error string `json:"error,omitempty"`
}
