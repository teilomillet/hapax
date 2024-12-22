package server

import (
	"context"

	"github.com/teilomillet/gollm"
	"github.com/teilomillet/gollm/llm"
	"github.com/teilomillet/gollm/utils"
)

// MockLLM implements a mock LLM for testing purposes
type MockLLM struct {
	GenerateFunc func(context.Context, *gollm.Prompt) (string, error)
	DebugFunc    func(string, ...interface{})
}

// NewMockLLM creates a new MockLLM with optional generate function
func NewMockLLM(generateFunc func(context.Context, *gollm.Prompt) (string, error)) *MockLLM {
	return &MockLLM{
		GenerateFunc: generateFunc,
	}
}

func (m *MockLLM) Generate(ctx context.Context, prompt *gollm.Prompt, opts ...llm.GenerateOption) (string, error) {
	if m.GenerateFunc != nil {
		return m.GenerateFunc(ctx, prompt)
	}
	return "", nil
}

func (m *MockLLM) Debug(format string, args ...interface{}) {
	if m.DebugFunc != nil {
		m.DebugFunc(format, args...)
	}
}

func (m *MockLLM) GetPromptJSONSchema(opts ...gollm.SchemaOption) ([]byte, error) {
	return []byte(`{}`), nil
}

func (m *MockLLM) GetProvider() string {
	return "mock"
}

func (m *MockLLM) GetModel() string {
	return "mock-model"
}

func (m *MockLLM) UpdateLogLevel(level gollm.LogLevel) {
	// No-op for mock
}

func (m *MockLLM) GetLogLevel() gollm.LogLevel {
	return gollm.LogLevelOff
}

func (m *MockLLM) SetLogLevel(level gollm.LogLevel) {
	// No-op for mock
}

func (m *MockLLM) GetLogger() utils.Logger {
	return utils.NewLogger(gollm.LogLevelOff)
}

func (m *MockLLM) NewPrompt(text string) *gollm.Prompt {
	return gollm.NewPrompt(text)
}

func (m *MockLLM) SetEndpoint(endpoint string) {
	// No-op for mock
}

func (m *MockLLM) SetOption(key string, value interface{}) {
	// No-op for mock
}

func (m *MockLLM) SupportsJSONSchema() bool {
	return false
}

func (m *MockLLM) GenerateWithSchema(ctx context.Context, prompt *gollm.Prompt, schema interface{}, opts ...llm.GenerateOption) (string, error) {
	return m.Generate(ctx, prompt, opts...)
}

func (m *MockLLM) SetOllamaEndpoint(endpoint string) error {
	// No-op for mock
	return nil
}

func (m *MockLLM) SetSystemPrompt(prompt string, cacheType llm.CacheType) {
	// No-op for mock
}
