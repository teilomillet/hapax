package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadValidConfig(t *testing.T) {
	yamlConfig := `
server:
  port: 9090
  read_timeout: 45s
  write_timeout: 45s
  max_header_bytes: 2097152
  shutdown_timeout: 45s

llm:
  provider: openai
  model: gpt-4
  endpoint: https://api.openai.com/v1
  system_prompt: "You are a helpful assistant."
  options:
    temperature: 0.8
    max_tokens: 4000

logging:
  level: debug
  format: json

routes:
  - path: /v1/completions
    handler: completion
  - path: /health
    handler: health
`

	config, err := Load(strings.NewReader(yamlConfig))
	if err != nil {
		t.Fatalf("Failed to load valid config: %v", err)
	}

	// Check server config
	if config.Server.Port != 9090 {
		t.Errorf("unexpected port: got %d, want %d", config.Server.Port, 9090)
	}
	if config.Server.ReadTimeout != 45*time.Second {
		t.Errorf("unexpected read timeout: got %v, want %v", config.Server.ReadTimeout, 45*time.Second)
	}

	// Check LLM config
	if config.LLM.Provider != "openai" {
		t.Errorf("unexpected provider: got %s, want %s", config.LLM.Provider, "openai")
	}
	if config.LLM.Model != "gpt-4" {
		t.Errorf("unexpected model: got %s, want %s", config.LLM.Model, "gpt-4")
	}

	// Check logging config
	if config.Logging.Level != "debug" {
		t.Errorf("unexpected log level: got %s, want %s", config.Logging.Level, "debug")
	}
	if config.Logging.Format != "json" {
		t.Errorf("unexpected log format: got %s, want %s", config.Logging.Format, "json")
	}

	// Check routes
	if len(config.Routes) != 2 {
		t.Errorf("unexpected number of routes: got %d, want %d", len(config.Routes), 2)
	}
}

func TestLoadInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{
			name: "invalid port",
			config: `
server:
  port: -1
`,
			want: "invalid port",
		},
		{
			name: "invalid log level",
			config: `
logging:
  level: invalid
`,
			want: "invalid log level",
		},
		{
			name: "empty provider",
			config: `
llm:
  provider: ""
`,
			want: "empty LLM provider",
		},
		{
			name: "empty route path",
			config: `
routes:
  - path: ""
    handler: test
`,
			want: "empty path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(strings.NewReader(tt.config))
			if err == nil {
				t.Error("expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("unexpected error: got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	// Check server defaults
	if config.Server.Port != 8080 {
		t.Errorf("unexpected default port: got %d, want %d", config.Server.Port, 8080)
	}
	if config.Server.ReadTimeout != 30*time.Second {
		t.Errorf("unexpected default read timeout: got %v, want %v", config.Server.ReadTimeout, 30*time.Second)
	}

	// Check LLM defaults
	if config.LLM.Provider != "ollama" {
		t.Errorf("unexpected default provider: got %s, want %s", config.LLM.Provider, "ollama")
	}
	if config.LLM.Model != "llama2" {
		t.Errorf("unexpected default model: got %s, want %s", config.LLM.Model, "llama2")
	}

	// Check logging defaults
	if config.Logging.Level != "info" {
		t.Errorf("unexpected default log level: got %s, want %s", config.Logging.Level, "info")
	}
	if config.Logging.Format != "json" {
		t.Errorf("unexpected default log format: got %s, want %s", config.Logging.Format, "json")
	}

	// Check default routes
	if len(config.Routes) != 2 {
		t.Errorf("unexpected number of default routes: got %d, want %d", len(config.Routes), 2)
	}
}
