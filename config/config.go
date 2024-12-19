package config

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the complete server configuration
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	LLM     LLMConfig    `yaml:"llm"`
	Logging LoggingConfig `yaml:"logging"`
	Routes  []RouteConfig `yaml:"routes"`
}

// ServerConfig holds server-specific configuration
type ServerConfig struct {
	Port            int           `yaml:"port"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	MaxHeaderBytes  int           `yaml:"max_header_bytes"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// LLMConfig holds LLM-specific configuration
type LLMConfig struct {
	Provider     string                 `yaml:"provider"`
	Model        string                 `yaml:"model"`
	APIKey       string                 `yaml:"api_key"`
	Endpoint     string                 `yaml:"endpoint"`
	SystemPrompt string                 `yaml:"system_prompt"`
	Options      map[string]interface{} `yaml:"options"`
}

// LoggingConfig holds logging-specific configuration
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// RouteConfig holds route-specific configuration
type RouteConfig struct {
	Path    string `yaml:"path"`
	Handler string `yaml:"handler"`
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:            8080,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			MaxHeaderBytes:  1 << 20, // 1MB
			ShutdownTimeout: 30 * time.Second,
		},
		LLM: LLMConfig{
			Provider: "ollama",
			Model:    "llama2",
			Endpoint: "http://localhost:11434",
			Options: map[string]interface{}{
				"temperature": 0.7,
				"max_tokens": 2000,
			},
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Routes: []RouteConfig{
			{Path: "/v1/completions", Handler: "completion"},
			{Path: "/health", Handler: "health"},
		},
	}
}

// LoadFile loads configuration from a YAML file
func LoadFile(filename string) (*Config, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()

	return Load(f)
}

// expandEnvVars replaces ${var} or $var in the string according to the values
// of the current environment variables. References to undefined variables are
// replaced by the empty string.
func expandEnvVars(s string) string {
	return os.ExpandEnv(s)
}

// Load loads configuration from an io.Reader
func Load(r io.Reader) (*Config, error) {
	// Read all bytes to expand environment variables
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand environment variables in the YAML
	expandedData := expandEnvVars(string(data))

	// Start with defaults
	config := DefaultConfig()

	// Decode YAML on top of defaults
	dec := yaml.NewDecoder(strings.NewReader(expandedData))
	if err := dec.Decode(config); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return config, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// Server validation
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Server.Port)
	}
	if c.Server.ReadTimeout < 0 {
		return fmt.Errorf("negative read timeout: %v", c.Server.ReadTimeout)
	}
	if c.Server.WriteTimeout < 0 {
		return fmt.Errorf("negative write timeout: %v", c.Server.WriteTimeout)
	}
	if c.Server.MaxHeaderBytes < 0 {
		return fmt.Errorf("negative max header bytes: %d", c.Server.MaxHeaderBytes)
	}
	if c.Server.ShutdownTimeout < 0 {
		return fmt.Errorf("negative shutdown timeout: %v", c.Server.ShutdownTimeout)
	}

	// LLM validation
	if c.LLM.Provider == "" {
		return fmt.Errorf("empty LLM provider")
	}
	if c.LLM.Model == "" {
		return fmt.Errorf("empty LLM model")
	}

	// Logging validation
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
		// Valid levels
	default:
		return fmt.Errorf("invalid log level: %s", c.Logging.Level)
	}

	switch c.Logging.Format {
	case "json", "text":
		// Valid formats
	default:
		return fmt.Errorf("invalid log format: %s", c.Logging.Format)
	}

	// Route validation
	for i, route := range c.Routes {
		if route.Path == "" {
			return fmt.Errorf("empty path in route %d", i)
		}
		if route.Handler == "" {
			return fmt.Errorf("empty handler in route %d", i)
		}
	}

	return nil
}
