package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnvironmentVariableExpansion tests various scenarios of environment variable expansion
func TestEnvironmentVariableExpansion(t *testing.T) {
	// Setup: Store original env vars and cleanup after
	originalEnv := os.Getenv("OPENAI_API_KEY")
	defer func() {
		os.Setenv("OPENAI_API_KEY", originalEnv)
	}()

	testCases := []struct {
		name       string
		envVars    map[string]string
		yamlConfig string
		validate   func(*testing.T, *Config)
		wantErr    bool
		errMsg     string
	}{
		{
			name: "basic env var expansion",
			envVars: map[string]string{
				"OPENAI_API_KEY": "test-key-123",
			},
			yamlConfig: `
llm:
    provider: openai
    api_key: ${OPENAI_API_KEY}
    model: gpt-4`,
			validate: func(t *testing.T, c *Config) {
				if c.LLM.APIKey != "test-key-123" {
					t.Errorf("API key not expanded correctly, got %s, want test-key-123", c.LLM.APIKey)
				}
			},
		},
		{
			name:    "missing env var",
			envVars: map[string]string{},
			yamlConfig: `
llm:
    provider: openai
    api_key: ${MISSING_API_KEY}
    model: gpt-4`,
			validate: func(t *testing.T, c *Config) {
				if c.LLM.APIKey != "" {
					t.Errorf("Missing env var should expand to empty string, got %s", c.LLM.APIKey)
				}
			},
		},
		{
			name: "multiple env vars in single value",
			envVars: map[string]string{
				"API_HOST":    "api.openai.com",
				"API_VERSION": "v1",
			},
			yamlConfig: `
llm:
    provider: openai
    endpoint: https://${API_HOST}/${API_VERSION}
    model: gpt-4`,
			validate: func(t *testing.T, c *Config) {
				expected := "https://api.openai.com/v1"
				if c.LLM.Endpoint != expected {
					t.Errorf("Multiple env vars not expanded correctly, got %s, want %s",
						c.LLM.Endpoint, expected)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup environment variables
			for k, v := range tc.envVars {
				if err := os.Setenv(k, v); err != nil {
					t.Fatalf("Failed to set env var %s: %v", k, err)
				}
			}

			// Load and validate config
			config, err := Load(strings.NewReader(tc.yamlConfig))

			if tc.wantErr {
				if err == nil {
					t.Errorf("Expected error containing %q, got nil", tc.errMsg)
				} else if !strings.Contains(err.Error(), tc.errMsg) {
					t.Errorf("Expected error containing %q, got %v", tc.errMsg, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Run validation
			tc.validate(t, config)

			// Cleanup environment variables
			for k := range tc.envVars {
				os.Unsetenv(k)
			}
		})
	}
}

// TestConfigValidationWithEnvVars tests config validation with environment variables
func TestConfigValidationWithEnvVars(t *testing.T) {
	testCases := []struct {
		name       string
		envVars    map[string]string
		yamlConfig string
		wantErr    bool
		errMsg     string
	}{
		{
			name: "valid config with env vars",
			envVars: map[string]string{
				"SERVER_PORT": "8080",
				"API_KEY":     "test-key",
			},
			yamlConfig: `
server:
    port: ${SERVER_PORT}
llm:
    provider: openai
    api_key: ${API_KEY}
    model: gpt-4`,
			wantErr: false,
		},
		{
			name: "invalid port from env var",
			envVars: map[string]string{
				"SERVER_PORT": "-1",
			},
			yamlConfig: `
server:
    port: ${SERVER_PORT}
llm:
    provider: openai
    model: gpt-4`,
			wantErr: true,
			errMsg:  "invalid port",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup environment
			for k, v := range tc.envVars {
				if err := os.Setenv(k, v); err != nil {
					t.Fatalf("Failed to set env var %s: %v", k, err)
				}
			}

			// Load config
			_, err := Load(strings.NewReader(tc.yamlConfig))

			// Verify error conditions
			if tc.wantErr {
				if err == nil {
					t.Errorf("Expected error containing %q, got nil", tc.errMsg)
				} else if !strings.Contains(err.Error(), tc.errMsg) {
					t.Errorf("Expected error containing %q, got %v", tc.errMsg, err)
				}
			} else if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Cleanup
			for k := range tc.envVars {
				os.Unsetenv(k)
			}
		})
	}
}

// TestConfigMerging tests how environment variables interact with default values
func TestConfigMerging(t *testing.T) {
	yamlConfig := `
llm:
    provider: ${PROVIDER}
    model: ${MODEL}
`
	// Setup environment
	envVars := map[string]string{
		"PROVIDER": "openai",
		// Intentionally not setting MODEL to test default value retention
	}

	for k, v := range envVars {
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("Failed to set env var %s: %v", k, err)
		}
		defer os.Unsetenv(k)
	}

	config, err := Load(strings.NewReader(yamlConfig))
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify provider was overridden but model retained default
	if config.LLM.Provider != "openai" {
		t.Errorf("Provider not set from env var, got %s, want openai", config.LLM.Provider)
	}
	if config.LLM.Model != "llama2" {
		t.Errorf("Model should retain default value, got %s, want llama2", config.LLM.Model)
	}
}

func TestConfigReloadWithEnvVars(t *testing.T) {
	// Test environment variable behavior during config reload
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
llm:
    provider: anthropic
    api_key: ${API_KEY}
    model: ${MODEL:-claude-3}`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Test reload behavior with environment changes
	os.Setenv("API_KEY", "initial-key")
	config, err := LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if config.LLM.APIKey != "initial-key" {
		t.Error("Initial environment variable not loaded")
	}

	// Change environment and reload
	os.Setenv("API_KEY", "new-key")
	// Simulate config file change
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify environment variable behavior during reload
	newConfig, err := LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if newConfig.LLM.APIKey != "new-key" {
		t.Error("Environment variable not updated during reload")
	}
}

func TestEnvironmentVariableHandling(t *testing.T) {
	// Original environment state management remains unchanged
	originalEnvVars := map[string]string{
		"ANTHROPIC_API_KEY": os.Getenv("ANTHROPIC_API_KEY"),
		"OPENAI_API_KEY":    os.Getenv("OPENAI_API_KEY"),
		"API_HOST":          os.Getenv("API_HOST"),
		"API_VERSION":       os.Getenv("API_VERSION"),
	}
	defer func() {
		for k, v := range originalEnvVars {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()

	testCases := []struct {
		name       string
		envVars    map[string]string
		yamlConfig string
		validate   func(*testing.T, *Config, map[string]string)
		setup      func() error
		cleanup    func()
		wantErr    bool
		errMsg     string
	}{
		{
			name: "api key with special characters",
			envVars: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-!@#$%^&*()_+=",
			},
			yamlConfig: `
llm:
    provider: anthropic
    api_key: ${ANTHROPIC_API_KEY}
    model: claude-3`,
			validate: func(t *testing.T, c *Config, envVars map[string]string) {
				if c.LLM.APIKey != "sk-ant-!@#$%^&*()_+=" {
					t.Errorf("Special characters in API key not preserved, got %s", c.LLM.APIKey)
				}
			},
		},
		{
			name: "nested environment variables",
			envVars: map[string]string{
				"API_HOST":    "api.anthropic.com",
				"API_VERSION": "v1",
				"FULL_URL":    "${API_HOST}/${API_VERSION}",
			},
			yamlConfig: `
llm:
    provider: anthropic
    endpoint: https://${FULL_URL}
    model: claude-3`,
			validate: func(t *testing.T, c *Config, envVars map[string]string) {
				expected := "https://api.anthropic.com/v1"
				if c.LLM.Endpoint != expected {
					t.Errorf("Nested environment variables not resolved correctly\nGot: %s\nWant: %s\nEnvironment: %v",
						c.LLM.Endpoint, expected, envVars)
				}
			},
		},
		{
			name: "multiple providers with different api keys",
			envVars: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-key123",
				"OPENAI_API_KEY":    "sk-key456",
			},
			yamlConfig: `
llm:
    provider: anthropic
    api_key: ${ANTHROPIC_API_KEY}
    model: claude-3
    backup_providers:
        - provider: openai
          api_key: ${OPENAI_API_KEY}
          model: gpt-4`,
			validate: func(t *testing.T, c *Config, envVars map[string]string) {
				if c.LLM.APIKey != "sk-ant-key123" {
					t.Errorf("Primary API key not set correctly, got %s", c.LLM.APIKey)
				}
				if len(c.LLM.BackupProviders) == 0 {
					t.Fatal("No backup providers configured")
				}
				if c.LLM.BackupProviders[0].APIKey != "sk-key456" {
					t.Errorf("Backup API key not set correctly, got %s", c.LLM.BackupProviders[0].APIKey)
				}
			},
		},
		{
			name: "environment variable case sensitivity",
			envVars: map[string]string{
				"api_key": "lowercase-key",
				"API_KEY": "uppercase-key",
			},
			yamlConfig: `
llm:
    provider: anthropic
    api_key: ${API_KEY}`,
			validate: func(t *testing.T, c *Config, envVars map[string]string) {
				if c.LLM.APIKey != "uppercase-key" {
					t.Errorf("Case sensitivity not handled correctly, got %s, want uppercase-key", c.LLM.APIKey)
				}
			},
		},
		{
			name:    "environment variable with default value",
			envVars: map[string]string{},
			yamlConfig: `
llm:
    provider: ${PROVIDER:-anthropic}
    model: ${MODEL:-claude-3}
    api_key: ${API_KEY:-default-key}`,
			validate: func(t *testing.T, c *Config, envVars map[string]string) {
				if c.LLM.Provider != "anthropic" {
					t.Errorf("Default value not applied correctly for provider\nGot: %s\nWant: anthropic\nEnvironment: %v",
						c.LLM.Provider, envVars)
				}
				if c.LLM.Model != "claude-3" {
					t.Errorf("Default value not applied correctly for model\nGot: %s\nWant: claude-3\nEnvironment: %v",
						c.LLM.Model, envVars)
				}
			},
		},
		{
			name: "empty environment variable handling",
			envVars: map[string]string{
				"EMPTY_KEY": "",
			},
			yamlConfig: `
llm:
    provider: anthropic
    api_key: ${EMPTY_KEY}`,
			validate: func(t *testing.T, c *Config, envVars map[string]string) {
				if c.LLM.APIKey != "" {
					t.Error("Empty environment variable should result in empty string")
				}
			},
		},
		{
			name: "environment variable in array element",
			envVars: map[string]string{
				"HANDLER_NAME": "completion",
			},
			yamlConfig: `
routes:
    - path: /v1/completions
      handler: ${HANDLER_NAME}
      version: v1`,
			validate: func(t *testing.T, c *Config, envVars map[string]string) {
				if len(c.Routes) == 0 {
					t.Fatal("No routes configured")
				}
				if c.Routes[0].Handler != "completion" {
					t.Errorf("Environment variable in array not expanded correctly, got %s, want completion",
						c.Routes[0].Handler)
				}
			},
		},
		{
			name: "invalid environment variable syntax",
			envVars: map[string]string{
				"VALID_KEY": "valid-value",
			},
			yamlConfig: `
llm:
    provider: anthropic
    api_key: ${VALID_KEY
    model: claude-3`,
			wantErr: true,
			errMsg:  "invalid syntax",
			validate: func(t *testing.T, c *Config, envVars map[string]string) {
				// This shouldn't be called if wantErr is true
				t.Error("Config loaded successfully despite invalid syntax")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				if err := tc.setup(); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			for k, v := range tc.envVars {
				if err := os.Setenv(k, v); err != nil {
					t.Fatalf("Failed to set env var %s: %v", k, err)
				}
			}

			config, err := Load(strings.NewReader(tc.yamlConfig))

			if tc.wantErr {
				if err == nil {
					t.Errorf("Expected error containing %q, got nil", tc.errMsg)
				} else if !strings.Contains(err.Error(), tc.errMsg) {
					t.Errorf("Expected error containing %q, got %v", tc.errMsg, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Updated to pass environment variables to validate function
			tc.validate(t, config, tc.envVars)

			if tc.cleanup != nil {
				tc.cleanup()
			}

			for k := range tc.envVars {
				os.Unsetenv(k)
			}
		})
	}
}
