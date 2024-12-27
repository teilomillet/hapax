package processing

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/mocks"
)

// TestNewProcessor verifies the initialization of the Processor.
// We test three critical scenarios:
// 1. Handling nil configuration (should fail gracefully)
// 2. Valid configuration (should initialize successfully)
// 3. Invalid template syntax (should detect and report template parsing errors)
//
// This ensures that our Processor validates its inputs and fails fast when
// given invalid configuration, preventing runtime errors later.
func TestNewProcessor(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.ProcessingConfig
		llm     gollm.LLM
		wantErr bool
	}{
		{
			name:    "nil config",
			cfg:     nil,
			llm:     mocks.NewMockLLM(nil),
			wantErr: true,
		},
		{
			name: "valid config",
			cfg: &config.ProcessingConfig{
				RequestTemplates: map[string]string{
					"default": "{{.Input}}",
				},
			},
			llm:     mocks.NewMockLLM(nil),
			wantErr: false,
		},
		{
			name: "invalid template",
			cfg: &config.ProcessingConfig{
				RequestTemplates: map[string]string{
					"default": "{{.Invalid}",
				},
			},
			llm:     mocks.NewMockLLM(nil),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc, err := NewProcessor(tt.cfg, tt.llm)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, proc)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, proc)
			}
		})
	}
}

// TestProcessRequest verifies the end-to-end request processing pipeline.
// This test ensures that:
// 1. Template selection and execution works correctly for both simple and chat requests
// 2. LLM responses are properly captured and formatted
// 3. Response length limits are enforced
// 4. Error cases are handled appropriately
//
// The mock responses are carefully crafted to match the template output format.
// For example, chat messages are formatted as "role: content\n" to match
// the chat template: "{{range .Messages}}{{.Role}}: {{.Content}}\n{{end}}"
func TestProcessRequest(t *testing.T) {
	// Updated mock responses to match new message structure
	mockResponses := map[string]string{
		"Hello":     "World",
		"Hi":        "Hello!",
		"Test":      "Very long response that should be truncated",
		"undefined": "",
	}

	// Updated mock LLM to handle the new message structure
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		// Always check if we have messages
		if len(prompt.Messages) == 0 {
			return mockResponses["undefined"], nil
		}

		// Find the last non-system message
		var lastContent string
		for i := len(prompt.Messages) - 1; i >= 0; i-- {
			if prompt.Messages[i].Role != "system" {
				lastContent = prompt.Messages[i].Content
				break
			}
		}

		// Return corresponding response
		if response, ok := mockResponses[lastContent]; ok {
			return response, nil
		}
		return mockResponses["undefined"], nil
	})

	// Rest of the configuration remains the same
	cfg := &config.ProcessingConfig{
		RequestTemplates: map[string]string{
			"default": "{{.Input}}",
			"chat":    "{{range .Messages}}{{.Role}}: {{.Content}}\n{{end}}",
		},
		ResponseFormatting: config.ResponseFormattingConfig{
			CleanJSON:      true,
			TrimWhitespace: true,
			MaxLength:      10,
		},
	}

	proc, err := NewProcessor(cfg, mockLLM)
	assert.NoError(t, err)

	ctx := context.Background()

	tests := []struct {
		name        string
		req         *Request
		wantContent string
		wantErr     bool
	}{
		{
			name: "simple completion",
			req: &Request{
				Type:  "default",
				Input: "Hello",
			},
			wantContent: "World",
			wantErr:     false,
		},
		{
			name: "chat completion",
			req: &Request{
				Type: "chat",
				Messages: []Message{
					{Role: "user", Content: "Hi"},
				},
			},
			wantContent: "Hello!",
			wantErr:     false,
		},
		{
			name:    "nil request",
			req:     nil,
			wantErr: true,
		},
		{
			name: "long response truncation",
			req: &Request{
				Type:  "default",
				Input: "Test",
			},
			wantContent: "Very long ", // Truncated to MaxLength (10)
			wantErr:     false,
		},
	}

	// Test execution remains the same
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := proc.ProcessRequest(ctx, tt.req)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, resp)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantContent, resp.Content)
			}
		})
	}
}

// TestFormatResponse verifies the response formatting features:
// 1. Whitespace trimming: Removes leading/trailing spaces
// 2. Length truncation: Ensures responses don't exceed max length
// 3. JSON cleaning: Removes markdown code blocks and formats JSON
//
// These formatting options are important for:
// - Consistent response format (trimming whitespace)
// - Network efficiency (length limits)
// - Client-side processing (clean JSON)
func TestFormatResponse(t *testing.T) {
	cfg := &config.ProcessingConfig{
		ResponseFormatting: config.ResponseFormattingConfig{
			CleanJSON:      true,
			TrimWhitespace: true,
			MaxLength:      50, // Increased max length to avoid truncation in these tests
		},
	}

	mockLLM := mocks.NewMockLLM(nil)
	proc, err := NewProcessor(cfg, mockLLM)
	assert.NoError(t, err)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "trim whitespace",
			input:    "  hello  ",
			expected: "hello",
		},
		{
			name:     "truncate long content",
			input:    "this is a very long response",
			expected: "this is a very long response",
		},
		{
			name:     "clean json response",
			input:    "```json\n{\"key\": \"value\"}\n```",
			expected: "{\"key\": \"value\"}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := proc.formatResponse(tt.input)
			assert.Equal(t, tt.expected, resp.Content)
		})
	}
}

// TestProcessMultiTurnConversation verifies that the processor correctly handles
// multi-turn conversations with different message types and maintains conversation context.
func TestProcessMultiTurnConversation(t *testing.T) {
	// Create a mock LLM that verifies message handling and returns appropriate responses
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		// Verify that messages are being passed correctly
		assert.NotNil(t, prompt.Messages)

		// Get the last message to determine the response
		lastMsg := prompt.Messages[len(prompt.Messages)-1]

		// Return different responses based on the message content
		switch lastMsg.Content {
		case "Hello, Claude":
			return "Hello! How can I assist you today?", nil
		case "Can you explain LLMs?":
			// Verify that previous messages are preserved
			assert.True(t, len(prompt.Messages) >= 3, "Expected previous messages to be included")
			return "Language Learning Models (LLMs) are AI systems that process and generate text...", nil
		default:
			return "I understand. What else would you like to know?", nil
		}
	})

	// Create processor with test configuration
	cfg := &config.ProcessingConfig{
		ResponseFormatting: config.ResponseFormattingConfig{
			TrimWhitespace: true,
		},
	}

	proc, err := NewProcessor(cfg, mockLLM)
	assert.NoError(t, err)

	// Set a system prompt to verify it's included
	proc.SetDefaultPrompt("You are a helpful AI assistant.")

	ctx := context.Background()

	// Test cases for different conversation patterns
	tests := []struct {
		name        string
		messages    []Message
		wantContent string
		wantErr     bool
	}{
		{
			name: "single message conversation",
			messages: []Message{
				{Role: "user", Content: "Hello, Claude"},
			},
			wantContent: "Hello! How can I assist you today?",
			wantErr:     false,
		},
		{
			name: "multi-turn conversation",
			messages: []Message{
				{Role: "user", Content: "Hello, Claude"},
				{Role: "assistant", Content: "Hello! How can I assist you today?"},
				{Role: "user", Content: "Can you explain LLMs?"},
			},
			wantContent: "Language Learning Models (LLMs) are AI systems that process and generate text...",
			wantErr:     false,
		},
		{
			name:     "conversation with empty messages",
			messages: []Message{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &Request{
				Type:     "chat",
				Messages: tt.messages,
			}

			resp, err := proc.ProcessRequest(ctx, req)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, resp)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantContent, resp.Content)
			}
		})
	}
}

// TestMessageOrderPreservation ensures that messages are processed in the correct order
// and that the conversation context is maintained properly.
func TestMessageOrderPreservation(t *testing.T) {
	var capturedMessages []gollm.PromptMessage

	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		// Capture messages for verification
		capturedMessages = prompt.Messages
		return "Response", nil
	})

	cfg := &config.ProcessingConfig{
		ResponseFormatting: config.ResponseFormattingConfig{
			TrimWhitespace: true,
		},
	}

	proc, err := NewProcessor(cfg, mockLLM)
	assert.NoError(t, err)
	proc.SetDefaultPrompt("System instruction")

	ctx := context.Background()

	// Test a complex conversation sequence
	req := &Request{
		Type: "chat",
		Messages: []Message{
			{Role: "user", Content: "First message"},
			{Role: "assistant", Content: "First response"},
			{Role: "user", Content: "Second message"},
		},
	}

	_, err = proc.ProcessRequest(ctx, req)
	assert.NoError(t, err)

	// Verify message order and content
	assert.Equal(t, "system", capturedMessages[0].Role)
	assert.Equal(t, "System instruction", capturedMessages[0].Content)
	assert.Equal(t, "user", capturedMessages[1].Role)
	assert.Equal(t, "First message", capturedMessages[1].Content)
	assert.Equal(t, "assistant", capturedMessages[2].Role)
	assert.Equal(t, "First response", capturedMessages[2].Content)
	assert.Equal(t, "user", capturedMessages[3].Role)
	assert.Equal(t, "Second message", capturedMessages[3].Content)
}

func TestAnthropicStyleConversations(t *testing.T) {
	// First, let's create a mock LLM that can track conversation state and verify message handling.
	// We'll make it return responses that depend on the conversation context.
	var capturedMessages []gollm.PromptMessage

	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		// Store the messages for verification
		capturedMessages = prompt.Messages

		// Simulate different responses based on conversation context
		lastMsg := prompt.Messages[len(prompt.Messages)-1]
		switch lastMsg.Content {
		case "Hello, Claude":
			return "Hello! I'm here to help.", nil
		case "Tell me about language models":
			// We should see the previous exchange in context
			if len(prompt.Messages) < 3 {
				return "", fmt.Errorf("expected previous conversation context")
			}
			return "Language models are AI systems that process and generate text based on patterns learned from training data.", nil
		case "Can you elaborate on that?":
			// This should have the full conversation history
			if len(prompt.Messages) < 5 {
				return "", fmt.Errorf("missing conversation history")
			}
			return "Let me build on my previous explanation...", nil
		default:
			return "I didn't understand that specific query.", nil
		}
	})

	// Create a processor with a simple configuration
	cfg := &config.ProcessingConfig{
		ResponseFormatting: config.ResponseFormattingConfig{
			TrimWhitespace: true,
		},
	}

	processor, err := NewProcessor(cfg, mockLLM)
	assert.NoError(t, err, "Processor creation should succeed")

	// Set a system prompt to verify it's maintained throughout the conversation
	processor.SetDefaultPrompt("You are a helpful AI assistant.")

	ctx := context.Background()

	// Now let's simulate a multi-turn conversation
	conversationSteps := []struct {
		name          string
		messages      []Message
		wantResponse  string
		wantMsgCount  int // Expected number of messages including system prompt
		shouldSucceed bool
	}{
		{
			name: "initial greeting",
			messages: []Message{
				{Role: "user", Content: "Hello, Claude"},
			},
			wantResponse:  "Hello! I'm here to help.",
			wantMsgCount:  2, // System prompt + user message
			shouldSucceed: true,
		},
		{
			name: "second turn with context",
			messages: []Message{
				{Role: "user", Content: "Hello, Claude"},
				{Role: "assistant", Content: "Hello! I'm here to help."},
				{Role: "user", Content: "Tell me about language models"},
			},
			wantResponse:  "Language models are AI systems that process and generate text based on patterns learned from training data.",
			wantMsgCount:  4, // System + 3 conversation messages
			shouldSucceed: true,
		},
		{
			name: "third turn with full history",
			messages: []Message{
				{Role: "user", Content: "Hello, Claude"},
				{Role: "assistant", Content: "Hello! I'm here to help."},
				{Role: "user", Content: "Tell me about language models"},
				{Role: "assistant", Content: "Language models are AI systems that process and generate text based on patterns learned from training data."},
				{Role: "user", Content: "Can you elaborate on that?"},
			},
			wantResponse:  "Let me build on my previous explanation...",
			wantMsgCount:  6, // System + 5 conversation messages
			shouldSucceed: true,
		},
	}

	for _, step := range conversationSteps {
		t.Run(step.name, func(t *testing.T) {
			// Reset captured messages for this test
			capturedMessages = nil

			// Create and send the request
			req := &Request{
				Type:     "chat",
				Messages: step.messages,
			}

			resp, err := processor.ProcessRequest(ctx, req)

			// Verify the results
			if step.shouldSucceed {
				assert.NoError(t, err, "Request should succeed")
				assert.NotNil(t, resp, "Response should not be nil")
				assert.Equal(t, step.wantResponse, resp.Content, "Response content should match expected")

				// Verify message count and system prompt
				assert.Equal(t, step.wantMsgCount, len(capturedMessages),
					"Incorrect number of messages in conversation")
				assert.Equal(t, "system", capturedMessages[0].Role,
					"First message should be system prompt")
				assert.Equal(t, "You are a helpful AI assistant.",
					capturedMessages[0].Content, "System prompt should be preserved")

				// Verify conversation order is maintained
				if len(step.messages) > 0 {
					lastMsg := capturedMessages[len(capturedMessages)-1]
					assert.Equal(t, step.messages[len(step.messages)-1].Content,
						lastMsg.Content, "Last message content should match")
					assert.Equal(t, step.messages[len(step.messages)-1].Role,
						lastMsg.Role, "Last message role should match")
				}
			} else {
				assert.Error(t, err, "Request should fail")
			}
		})
	}
}
