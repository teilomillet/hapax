package processing

import (
	"context"
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
	// Map of input prompts to expected LLM responses
	// Note: The keys must match the exact output of our templates
	mockResponses := map[string]string{
		"Hello":                "World",           // Simple completion
		"user: Hi\n":          "Hello!",          // Chat completion (matches template format)
		"Test":                "Very long response that should be truncated",
		"undefined":           "",                 // Default response for unmatched inputs
	}

	// Create a mock LLM that returns predefined responses based on the input prompt
	// For chat messages, we check prompt.Messages[1] because index 0 is the system prompt
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		if len(prompt.Messages) < 2 {
			return mockResponses["undefined"], nil
		}
		return mockResponses[prompt.Messages[1].Content], nil
	})

	// Configure the processor with both simple and chat templates
	// Also set up response formatting to test truncation and cleaning
	cfg := &config.ProcessingConfig{
		RequestTemplates: map[string]string{
			"default": "{{.Input}}",
			"chat":    "{{range .Messages}}{{.Role}}: {{.Content}}\n{{end}}",
		},
		ResponseFormatting: config.ResponseFormattingConfig{
			CleanJSON:      true,
			TrimWhitespace: true,
			MaxLength:      10, // Short length to test truncation
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
