package mocks

import (
	"context"
	"time"

	"github.com/teilomillet/gollm"
	"github.com/teilomillet/gollm/llm"
	"github.com/teilomillet/gollm/utils"
)

// MockLLM implements a mock LLM for testing purposes.
// It provides a flexible way to simulate LLM behavior in tests without making actual API calls.
//
// Key features:
// 1. Configurable response generation through GenerateFunc
// 2. Debug logging capture through DebugFunc
// 3. Default implementations for all interface methods
//
// Example usage:
//
//	mockLLM := NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
//	    return "mocked response", nil
//	})
type MockLLM struct {
	GenerateFunc func(context.Context, *gollm.Prompt) (string, error)
	DebugFunc   func(string, ...interface{})
}

// NewMockLLM creates a new MockLLM with optional generate function.
// If generateFunc is nil, Generate will return empty string with no error.
func NewMockLLM(generateFunc func(context.Context, *gollm.Prompt) (string, error)) *MockLLM {
	return &MockLLM{
		GenerateFunc: generateFunc,
	}
}

// Generate implements the core LLM functionality.
// It uses the provided GenerateFunc if available, otherwise returns empty string.
// The opts parameter is ignored in the mock to simplify testing.
func (m *MockLLM) Generate(ctx context.Context, prompt *gollm.Prompt, opts ...llm.GenerateOption) (string, error) {
	// Check for timeout header
	if ctx.Value("X-Test-Timeout") != nil {
		// Sleep longer than the timeout
		time.Sleep(10 * time.Second)
		return "", context.DeadlineExceeded
	}

	if m.GenerateFunc != nil {
		return m.GenerateFunc(ctx, prompt)
	}
	return "", nil
}

// Debug captures debug messages if DebugFunc is provided.
// This allows tests to verify logging behavior if needed.
func (m *MockLLM) Debug(format string, args ...interface{}) {
	if m.DebugFunc != nil {
		m.DebugFunc(format, args...)
	}
}

// GetPromptJSONSchema returns a minimal valid JSON schema.
// This is useful for testing schema validation without complex schemas.
func (m *MockLLM) GetPromptJSONSchema(opts ...gollm.SchemaOption) ([]byte, error) {
	return []byte(`{}`), nil
}

// GetProvider returns a mock provider name.
// This helps identify mock instances in logs and debugging.
func (m *MockLLM) GetProvider() string {
	return "mock"
}

// GetModel returns a mock model name.
// This helps identify mock instances in logs and debugging.
func (m *MockLLM) GetModel() string {
	return "mock-model"
}

// GetLogLevel returns a default log level.
// Tests can rely on this consistent behavior.
func (m *MockLLM) GetLogLevel() gollm.LogLevel {
	return gollm.LogLevelInfo
}

// UpdateLogLevel is a no-op in the mock.
// Real implementation would change logging behavior.
func (m *MockLLM) UpdateLogLevel(level gollm.LogLevel) {
	// No-op for mock
}

// SetLogLevel is a no-op in the mock.
// Real implementation would change logging behavior.
func (m *MockLLM) SetLogLevel(level gollm.LogLevel) {
	// No-op for mock
}

// GetLogger returns nil as we don't need logging in tests.
// Real implementation would return a logger instance.
func (m *MockLLM) GetLogger() utils.Logger {
	return nil
}

// NewPrompt creates a simple prompt with user role.
// This provides consistent prompt creation for tests.
func (m *MockLLM) NewPrompt(text string) *gollm.Prompt {
	return &gollm.Prompt{
		Messages: []gollm.PromptMessage{
			{Role: "user", Content: text},
		},
	}
}

// SetEndpoint is a no-op in the mock.
// Real implementation would configure the API endpoint.
func (m *MockLLM) SetEndpoint(endpoint string) {
	// No-op for mock
}

// SetOption is a no-op in the mock.
// Real implementation would configure LLM options.
func (m *MockLLM) SetOption(key string, value interface{}) {
	// No-op for mock
}

// SupportsJSONSchema returns true to indicate schema support.
// This allows testing schema-related functionality.
func (m *MockLLM) SupportsJSONSchema() bool {
	return true
}

// GenerateWithSchema uses the standard Generate function.
// Schema validation is not performed in the mock.
func (m *MockLLM) GenerateWithSchema(ctx context.Context, prompt *gollm.Prompt, schema interface{}, opts ...llm.GenerateOption) (string, error) {
	if m.GenerateFunc != nil {
		return m.GenerateFunc(ctx, prompt)
	}
	return "", nil
}

// SetOllamaEndpoint is a no-op in the mock.
// Real implementation would configure Ollama endpoint.
func (m *MockLLM) SetOllamaEndpoint(endpoint string) error {
	return nil
}

// SetSystemPrompt is a no-op in the mock.
// Real implementation would set a system-level prompt.
func (m *MockLLM) SetSystemPrompt(prompt string, cacheType llm.CacheType) {
	// No-op for mock
}
