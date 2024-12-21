// Package config provides configuration management for the Hapax LLM server.
// It includes support for various LLM providers, token validation, caching,
// and runtime behavior customization.
package config

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the complete server configuration.
// It combines server settings, LLM configuration, logging preferences,
// and route definitions into a single, cohesive configuration structure.
type Config struct {
	Server             ServerConfig              `yaml:"server"`
	LLM                LLMConfig                 `yaml:"llm"`
	Logging            LoggingConfig             `yaml:"logging"`
	Routes             []RouteConfig             `yaml:"routes"`
	Providers          map[string]ProviderConfig `yaml:"providers"`
	ProviderPreference []string                  `yaml:"provider_preference"` // Order of provider preference
	CircuitBreaker     CircuitBreakerConfig      `yaml:"circuit_breaker"`
	TestMode           bool                      `yaml:"-"` // Skip provider initialization in tests
}

// ServerConfig holds server-specific configuration for the HTTP server.
// It defines timeouts, limits, and operational parameters.
type ServerConfig struct {
	// Port specifies the HTTP server port (default: 8080)
	Port int `yaml:"port"`

	// ReadTimeout is the maximum duration for reading the entire request,
	// including the body (default: 30s)
	ReadTimeout time.Duration `yaml:"read_timeout"`

	// WriteTimeout is the maximum duration before timing out writes of the response
	// (default: 30s)
	WriteTimeout time.Duration `yaml:"write_timeout"`

	// MaxHeaderBytes controls the maximum number of bytes the server will
	// read parsing the request header's keys and values (default: 1MB)
	MaxHeaderBytes int `yaml:"max_header_bytes"`

	// ShutdownTimeout specifies how long to wait for the server to shutdown
	// gracefully before forcing termination (default: 30s)
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// LLMConfig holds LLM-specific configuration.
// It supports multiple providers (OpenAI, Anthropic, Ollama) and includes
// settings for token validation, caching, and generation parameters.
type LLMConfig struct {
	// Provider specifies the LLM provider (e.g., "openai", "anthropic", "ollama")
	Provider string `yaml:"provider"`

	// Model is the name of the model to use (e.g., "gpt-4", "claude-3-haiku")
	Model string `yaml:"model"`

	// APIKey is the authentication key for the provider's API
	// Use environment variables (e.g., ${OPENAI_API_KEY}) for secure configuration
	APIKey string `yaml:"api_key"`

	// Endpoint is the API endpoint URL
	// For Ollama, this is typically "http://localhost:11434"
	Endpoint string `yaml:"endpoint"`

	// SystemPrompt is the default system prompt to use
	SystemPrompt string `yaml:"system_prompt"`

	// MaxContextTokens is the maximum number of tokens in the context window
	// This varies by model:
	// - GPT-4: 8192 or 32768
	// - Claude: 100k
	// - Llama2: Varies by version
	MaxContextTokens int `yaml:"max_context_tokens"`

	// Cache configuration (optional)
	Cache *CacheConfig `yaml:"cache,omitempty"`

	// Retry configuration (optional)
	Retry *RetryConfig `yaml:"retry,omitempty"`

	// Options contains provider-specific generation parameters
	Options map[string]interface{} `yaml:"options"`

	// BackupProviders defines failover providers (optional)
	BackupProviders []BackupProvider `yaml:"backup_providers,omitempty"`

	// HealthCheck defines provider health monitoring settings (optional)
	HealthCheck *ProviderHealthCheck `yaml:"health_check,omitempty"`
}

// BackupProvider defines a fallback LLM provider
type BackupProvider struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	APIKey   string `yaml:"api_key"`
}

// ProviderHealthCheck defines health check settings
type ProviderHealthCheck struct {
	Enabled          bool          `yaml:"enabled"`
	Interval         time.Duration `yaml:"interval"`
	Timeout          time.Duration `yaml:"timeout"`
	FailureThreshold int           `yaml:"failure_threshold"`
}

// CacheConfig defines caching behavior for LLM responses.
// Caching can significantly improve performance and reduce API costs
// by storing and reusing responses for identical prompts.
type CacheConfig struct {
	// Enable turns caching on/off (default: false)
	Enable bool `yaml:"enable"`

	// Type specifies the caching strategy:
	// - "memory": In-memory cache (cleared on restart)
	// - "redis": Redis-based persistent cache
	// - "file": File-based persistent cache
	Type string `yaml:"type"`

	// TTL specifies how long to keep cached responses (default: 24h)
	TTL time.Duration `yaml:"ttl"`

	// MaxSize limits the cache size:
	// - For memory cache: maximum number of entries
	// - For file cache: maximum total size in bytes
	MaxSize int64 `yaml:"max_size"`

	// Dir specifies the directory for file-based cache
	Dir string `yaml:"dir,omitempty"`

	// Redis configuration (only used if Type is "redis")
	Redis *RedisCacheConfig `yaml:"redis,omitempty"`
}

// RedisCacheConfig holds Redis-specific cache configuration.
type RedisCacheConfig struct {
	// Address is the Redis server address (e.g., "localhost:6379")
	Address string `yaml:"address"`

	// Password for Redis authentication (optional)
	Password string `yaml:"password"`

	// DB is the Redis database number to use
	DB int `yaml:"db"`
}

// RetryConfig defines the retry behavior for failed API calls.
// This helps handle transient errors and rate limiting gracefully.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts (default: 3)
	MaxRetries int `yaml:"max_retries"`

	// InitialDelay is the delay before the first retry (default: 1s)
	InitialDelay time.Duration `yaml:"initial_delay"`

	// MaxDelay caps the maximum delay between retries (default: 30s)
	MaxDelay time.Duration `yaml:"max_delay"`

	// Multiplier increases the delay after each retry (default: 2)
	// The delay pattern will be: initial_delay * (multiplier ^ retry_count)
	Multiplier float64 `yaml:"multiplier"`

	// RetryableErrors specifies which error types should trigger retries
	// Common values: "rate_limit", "timeout", "server_error"
	RetryableErrors []string `yaml:"retryable_errors"`
}

// ProviderConfig holds configuration for an LLM provider
type ProviderConfig struct {
	Type   string `yaml:"type"`    // Provider type (e.g., openai, anthropic)
	Model  string `yaml:"model"`   // Model name
	APIKey string `yaml:"api_key"` // API key for authentication
}

// LoggingConfig holds logging-specific configuration.
type LoggingConfig struct {
	// Level sets logging verbosity: debug, info, warn, error
	Level string `yaml:"level"`

	// Format specifies log output format: json or text
	Format string `yaml:"format"`
}

// RouteConfig holds route-specific configuration.
type RouteConfig struct {
	// Path is the URL path to match
	Path string `yaml:"path"`

	// Handler specifies which handler to use for this route
	Handler string `yaml:"handler"`

	// Version specifies the API version (e.g., "v1", "v2")
	Version string `yaml:"version"`

	// Methods specifies the allowed HTTP methods for this route
	Methods []string `yaml:"methods"`

	// Headers specifies the required headers for this route
	Headers map[string]string `yaml:"headers,omitempty"`

	// Middleware specifies the route-specific middleware
	Middleware []string `yaml:"middleware,omitempty"`

	// HealthCheck specifies the health check configuration for this route
	HealthCheck *HealthCheck `yaml:"health_check,omitempty"`
}

// HealthCheck defines health check configuration for a route
type HealthCheck struct {
	// Enabled specifies whether health checks are enabled for this route
	Enabled bool `yaml:"enabled"`

	// Interval specifies the interval between health checks
	Interval time.Duration `yaml:"interval"`

	// Timeout specifies the timeout for health checks
	Timeout time.Duration `yaml:"timeout"`

	// Threshold specifies the number of failures before marking the route as unhealthy
	Threshold int `yaml:"threshold"`

	// Checks specifies the map of check name to check type
	Checks map[string]string `yaml:"checks"`
}

type CircuitBreakerConfig struct {
	// MaxRequests is maximum number of requests allowed to pass through when in half-open state
	MaxRequests uint32 `yaml:"max_requests"`

	// Interval is the cyclic period of the closed state for the circuit breaker
	Interval time.Duration `yaml:"interval"`

	// Timeout is the period of the open state until it becomes half-open
	Timeout time.Duration `yaml:"timeout"`

	// FailureThreshold is the number of failures needed to trip the circuit
	FailureThreshold uint32 `yaml:"failure_threshold"`

	// TestMode indicates whether to skip Prometheus metric registration (for testing)
	TestMode bool `yaml:"test_mode"`
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
			HealthCheck: &ProviderHealthCheck{
				Enabled:          true,
				Interval:         30 * time.Second,
				Timeout:          5 * time.Second,
				FailureThreshold: 3,
			},
			Cache: &CacheConfig{
				Enable:  false,
				Type:    "memory",
				TTL:     24 * time.Hour,
				MaxSize: 1 << 30, // 1GB
			},
			Retry: &RetryConfig{
				MaxRetries:      3,
				InitialDelay:    100 * time.Millisecond,
				MaxDelay:        2 * time.Second,
				Multiplier:      2.0,
				RetryableErrors: []string{"rate_limit", "timeout", "server_error"},
			},
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Routes: []RouteConfig{
			{
				Path:    "/v1/completions",
				Handler: "completion",
				Version: "v1",
			},
			{
				Path:    "/health",
				Handler: "health",
				Version: "v1",
			},
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
	if c.LLM.MaxContextTokens < 0 {
		return fmt.Errorf("negative max context tokens: %d", c.LLM.MaxContextTokens)
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
		if route.Version == "" {
			return fmt.Errorf("empty version in route %d", i)
		}
	}

	return nil
}
