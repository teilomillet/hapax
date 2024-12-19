package validation

import (
	"fmt"
	"time"

	"github.com/pkoukk/tiktoken-go"
)

// Tokenizer defines the interface for token counting
type Tokenizer interface {
	Encode(text string, allowedSpecial, disallowedSpecial []string) []int
	Decode(tokens []int) string
	CountTokens(text string) int
}

// tiktokenWrapper wraps tiktoken to implement our Tokenizer interface
type tiktokenWrapper struct {
	*tiktoken.Tiktoken
}

func (t *tiktokenWrapper) CountTokens(text string) int {
	tokens := t.Encode(text, nil, nil)
	return len(tokens)
}

// CompletionRequest represents the expected schema for completion requests
type CompletionRequest struct {
	Messages []Message `json:"messages" validate:"required,dive"`
	Options  *Options  `json:"options,omitempty" validate:"omitempty"`
}

// Message represents a single message in a completion request
type Message struct {
	Role    string `json:"role" validate:"required,oneof=user assistant system"`
	Content string `json:"content" validate:"required"`
}

// Options represents optional parameters for completion requests
type Options struct {
	Temperature      float64 `json:"temperature,omitempty" validate:"omitempty,gte=0,lte=1"`
	MaxTokens       int     `json:"max_tokens,omitempty" validate:"omitempty,gt=0"`
	TopP            float64 `json:"top_p,omitempty" validate:"omitempty,gt=0,lte=1"`
	FrequencyPenalty float64 `json:"frequency_penalty,omitempty" validate:"omitempty,gte=-2,lte=2"`
	PresencePenalty  float64 `json:"presence_penalty,omitempty" validate:"omitempty,gte=-2,lte=2"`
	Cache           *CacheOptions `json:"cache,omitempty" validate:"omitempty"`
	Retry           *RetryOptions `json:"retry,omitempty" validate:"omitempty"`
}

// CacheOptions represents caching configuration for requests
type CacheOptions struct {
	Enable   bool          `json:"enable"`
	Type     string        `json:"type" validate:"omitempty,oneof=memory redis file"`
	TTL      time.Duration `json:"ttl" validate:"omitempty,gt=0"`
	MaxSize  int64         `json:"max_size" validate:"omitempty,gt=0"`
	Dir      string        `json:"dir" validate:"omitempty,required_if=Type file,dir"`
	Redis    *RedisOptions `json:"redis" validate:"omitempty,required_if=Type redis"`
}

// RedisOptions represents Redis-specific configuration
type RedisOptions struct {
	Address  string `json:"address" validate:"required,hostname_port"`
	Password string `json:"password" validate:"omitempty"`
	DB       int    `json:"db" validate:"gte=0"`
}

// RetryOptions represents retry configuration for failed requests
type RetryOptions struct {
	MaxRetries      int           `json:"max_retries" validate:"gt=0"`
	InitialDelay    time.Duration `json:"initial_delay" validate:"required,gt=0"`
	MaxDelay        time.Duration `json:"max_delay" validate:"required,gtfield=InitialDelay"`
	Multiplier      float64       `json:"multiplier" validate:"gt=1"`
	RetryableErrors []string      `json:"retryable_errors" validate:"required,min=1,dive,oneof=rate_limit timeout server_error"`
}

// TokenCounter handles token counting for messages using tiktoken
type TokenCounter struct {
	encoding Tokenizer
}

// NewTokenCounter creates a new token counter for the specified model
func NewTokenCounter(model string) (*TokenCounter, error) {
	encoding, err := tiktoken.EncodingForModel(model)
	if err != nil {
		return nil, fmt.Errorf("failed to get encoding for model %s: %v", model, err)
	}
	return &TokenCounter{encoding: &tiktokenWrapper{encoding}}, nil
}

// CountTokens counts the total number of tokens in a message
func (tc *TokenCounter) CountTokens(msg Message) int {
	return tc.encoding.CountTokens(msg.Content)
}

// CountRequestTokens counts the total number of tokens in a completion request
func (tc *TokenCounter) CountRequestTokens(req CompletionRequest) int {
	total := 0
	for _, msg := range req.Messages {
		total += tc.CountTokens(msg)
	}
	return total
}

// ValidateTokens checks if the request's token count is within limits
func (tc *TokenCounter) ValidateTokens(req CompletionRequest, maxContextTokens int) error {
	if maxContextTokens <= 0 {
		return fmt.Errorf("invalid max_context_tokens: must be greater than 0")
	}

	totalTokens := tc.CountRequestTokens(req)
	if req.Options != nil && req.Options.MaxTokens > 0 {
		totalTokens += req.Options.MaxTokens
	}

	if totalTokens > maxContextTokens {
		return fmt.Errorf("total tokens (%d) exceeds max context length (%d)", totalTokens, maxContextTokens)
	}

	return nil
}

// ValidateOptions performs comprehensive validation of request options
func ValidateOptions(opts *Options) error {
	if opts == nil {
		return nil
	}

	var errs []error

	// Validate generation parameters
	if opts.Temperature < 0 || opts.Temperature > 1 {
		errs = append(errs, fmt.Errorf("temperature must be between 0 and 1"))
	}
	if opts.TopP <= 0 || opts.TopP > 1 {
		errs = append(errs, fmt.Errorf("top_p must be between 0 and 1"))
	}
	if opts.FrequencyPenalty < -2 || opts.FrequencyPenalty > 2 {
		errs = append(errs, fmt.Errorf("frequency_penalty must be between -2 and 2"))
	}
	if opts.PresencePenalty < -2 || opts.PresencePenalty > 2 {
		errs = append(errs, fmt.Errorf("presence_penalty must be between -2 and 2"))
	}

	// Validate cache options
	if opts.Cache != nil {
		if err := validateCacheOptions(opts.Cache); err != nil {
			errs = append(errs, err)
		}
	}

	// Validate retry options
	if opts.Retry != nil {
		if err := validateRetryOptions(opts.Retry); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("validation errors: %v", errs)
	}

	return nil
}

// validateCacheOptions validates cache-specific configuration
func validateCacheOptions(cache *CacheOptions) error {
	if !cache.Enable {
		return nil
	}

	var errs []error

	switch cache.Type {
	case "memory":
		if cache.MaxSize <= 0 {
			errs = append(errs, fmt.Errorf("max_size must be greater than 0 for memory cache"))
		}
	case "redis":
		if cache.Redis == nil {
			errs = append(errs, fmt.Errorf("redis configuration required when cache type is 'redis'"))
		}
	case "file":
		if cache.Dir == "" {
			errs = append(errs, fmt.Errorf("directory path required when cache type is 'file'"))
		}
	default:
		errs = append(errs, fmt.Errorf("invalid cache type: must be one of [memory, redis, file]"))
	}

	if cache.TTL <= 0 {
		errs = append(errs, fmt.Errorf("cache TTL must be greater than 0"))
	}

	if len(errs) > 0 {
		return fmt.Errorf("cache validation errors: %v", errs)
	}

	return nil
}

// validateRetryOptions validates retry-specific configuration
func validateRetryOptions(retry *RetryOptions) error {
	var errs []error

	if retry.MaxRetries <= 0 {
		errs = append(errs, fmt.Errorf("max_retries must be greater than 0"))
	}
	if retry.InitialDelay <= 0 {
		errs = append(errs, fmt.Errorf("initial_delay must be greater than 0"))
	}
	if retry.MaxDelay <= retry.InitialDelay {
		errs = append(errs, fmt.Errorf("max_delay must be greater than initial_delay"))
	}
	if retry.Multiplier <= 1 {
		errs = append(errs, fmt.Errorf("multiplier must be greater than 1"))
	}

	validErrors := map[string]bool{
		"rate_limit":    true,
		"timeout":       true,
		"server_error": true,
	}

	for _, errType := range retry.RetryableErrors {
		if !validErrors[errType] {
			errs = append(errs, fmt.Errorf("invalid retry error type: %s", errType))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("retry validation errors: %v", errs)
	}

	return nil
}
