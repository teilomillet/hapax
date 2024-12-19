package validation

import (
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
)

// mockTiktoken implements a mock tokenizer for testing
type mockTiktoken struct {
	countTokens func(string) int
}

func (m *mockTiktoken) Encode(text string, allowedSpecial, disallowedSpecial []string) []int {
	tokens := make([]int, m.countTokens(text))
	for i := range tokens {
		tokens[i] = i
	}
	return tokens
}

func (m *mockTiktoken) Decode(tokens []int) string {
	return ""
}

func (m *mockTiktoken) CountTokens(text string) int {
	return m.countTokens(text)
}

func TestCompletionRequestValidation(t *testing.T) {
	validate := validator.New()

	tests := []struct {
		name    string
		req     CompletionRequest
		wantErr bool
	}{
		{
			name: "valid request",
			req: CompletionRequest{
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid request with options",
			req: CompletionRequest{
				Messages: []Message{
					{Role: "system", Content: "You are helpful."},
					{Role: "user", Content: "Hello"},
				},
				Options: &Options{
					Temperature: 0.7,
					MaxTokens:   1000,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid role",
			req: CompletionRequest{
				Messages: []Message{
					{Role: "invalid", Content: "Hello"},
				},
			},
			wantErr: true,
		},
		{
			name: "empty content",
			req: CompletionRequest{
				Messages: []Message{
					{Role: "user", Content: ""},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid temperature",
			req: CompletionRequest{
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
				Options: &Options{
					Temperature: 1.5,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.req)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTokenValidation(t *testing.T) {
	counter := &TokenCounter{
		encoding: &mockTiktoken{
			countTokens: func(s string) int {
				// Each word and space is a token, plus one for the role
				s = strings.TrimSpace(s) // Remove trailing space
				words := strings.Split(s, " ")
				if len(words) == 0 {
					return 1 // Just the role token
				}
				return len(words) + (len(words) - 1) + 1 // words + spaces between words + role token
			},
		},
	}

	tests := []struct {
		name           string
		req            CompletionRequest
		maxContext     int
		expectedError  string
		expectNoError  bool
	}{
		{
			name: "valid request within limits",
			req: CompletionRequest{
				Messages: []Message{
					{Role: "user", Content: "Hello world"},
				},
			},
			maxContext:    100,
			expectNoError: true,
		},
		{
			name: "exceeds max context",
			req: CompletionRequest{
				Messages: []Message{
					{Role: "user", Content: strings.TrimSpace(strings.Repeat("word ", 33))}, // 33 words + 32 spaces + 1 role = 66 tokens
				},
			},
			maxContext:    65,
			expectedError: "total tokens (66) exceeds max context length (65)",
		},
		{
			name: "max tokens would exceed context",
			req: CompletionRequest{
				Messages: []Message{
					{Role: "user", Content: strings.TrimSpace(strings.Repeat("word ", 28))}, // 28 words + 27 spaces + 1 role = 56 tokens
				},
				Options: &Options{
					MaxTokens: 10,
				},
			},
			maxContext:    65,
			expectedError: "total tokens (66) exceeds max context length (65)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := counter.ValidateTokens(tt.req, tt.maxContext)
			if tt.expectNoError {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

func TestValidateOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    *Options
		wantErr bool
	}{
		{
			name:    "nil options",
			opts:    nil,
			wantErr: false,
		},
		{
			name: "valid options",
			opts: &Options{
				Temperature:      0.7,
				MaxTokens:        1000,
				TopP:             0.9,
				FrequencyPenalty: 0.5,
				PresencePenalty:  0.5,
			},
			wantErr: false,
		},
		{
			name: "invalid temperature",
			opts: &Options{
				Temperature: 1.5,
			},
			wantErr: true,
		},
		{
			name: "invalid top_p",
			opts: &Options{
				TopP: -0.1,
			},
			wantErr: true,
		},
		{
			name: "invalid frequency penalty",
			opts: &Options{
				FrequencyPenalty: 2.5,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOptions(tt.opts)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCacheOptions(t *testing.T) {
	tests := []struct {
		name    string
		cache   *CacheOptions
		wantErr bool
	}{
		{
			name: "valid memory cache",
			cache: &CacheOptions{
				Enable:  true,
				Type:    "memory",
				TTL:     time.Hour,
				MaxSize: 1000,
			},
			wantErr: false,
		},
		{
			name: "valid redis cache",
			cache: &CacheOptions{
				Enable: true,
				Type:   "redis",
				TTL:    time.Hour,
				Redis: &RedisOptions{
					Address: "localhost:6379",
					DB:      0,
				},
			},
			wantErr: false,
		},
		{
			name: "valid file cache",
			cache: &CacheOptions{
				Enable: true,
				Type:   "file",
				TTL:    time.Hour,
				Dir:    "/tmp/cache",
			},
			wantErr: false,
		},
		{
			name: "invalid cache type",
			cache: &CacheOptions{
				Enable: true,
				Type:   "invalid",
			},
			wantErr: true,
		},
		{
			name: "missing redis config",
			cache: &CacheOptions{
				Enable: true,
				Type:   "redis",
				TTL:    time.Hour,
			},
			wantErr: true,
		},
		{
			name: "missing file directory",
			cache: &CacheOptions{
				Enable: true,
				Type:   "file",
				TTL:    time.Hour,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCacheOptions(tt.cache)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRetryOptions(t *testing.T) {
	tests := []struct {
		name    string
		retry   *RetryOptions
		wantErr bool
	}{
		{
			name: "valid retry options",
			retry: &RetryOptions{
				MaxRetries:      3,
				InitialDelay:    time.Second,
				MaxDelay:        time.Second * 10,
				Multiplier:      2.0,
				RetryableErrors: []string{"rate_limit", "timeout"},
			},
			wantErr: false,
		},
		{
			name: "invalid max retries",
			retry: &RetryOptions{
				MaxRetries:      0,
				InitialDelay:    time.Second,
				MaxDelay:        time.Second * 10,
				Multiplier:      2.0,
				RetryableErrors: []string{"rate_limit"},
			},
			wantErr: true,
		},
		{
			name: "invalid initial delay",
			retry: &RetryOptions{
				MaxRetries:      3,
				InitialDelay:    0,
				MaxDelay:        time.Second * 10,
				Multiplier:      2.0,
				RetryableErrors: []string{"rate_limit"},
			},
			wantErr: true,
		},
		{
			name: "invalid max delay",
			retry: &RetryOptions{
				MaxRetries:      3,
				InitialDelay:    time.Second * 10,
				MaxDelay:        time.Second,
				Multiplier:      2.0,
				RetryableErrors: []string{"rate_limit"},
			},
			wantErr: true,
		},
		{
			name: "invalid multiplier",
			retry: &RetryOptions{
				MaxRetries:      3,
				InitialDelay:    time.Second,
				MaxDelay:        time.Second * 10,
				Multiplier:      0.5,
				RetryableErrors: []string{"rate_limit"},
			},
			wantErr: true,
		},
		{
			name: "invalid error type",
			retry: &RetryOptions{
				MaxRetries:      3,
				InitialDelay:    time.Second,
				MaxDelay:        time.Second * 10,
				Multiplier:      2.0,
				RetryableErrors: []string{"invalid_error"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRetryOptions(tt.retry)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
