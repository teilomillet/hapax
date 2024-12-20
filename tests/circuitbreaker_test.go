package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/circuitbreaker"
	"github.com/teilomillet/hapax/server/mocks"
	"github.com/teilomillet/hapax/server/provider"
	"github.com/teilomillet/gollm"
	"go.uber.org/zap"
)

func TestCircuitBreaker(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	registry := prometheus.NewRegistry()

	config := circuitbreaker.Config{
		FailureThreshold:  2,         // Trip after 2 failures
		ResetTimeout:     time.Second, // Short timeout for testing
		HalfOpenRequests: 1,
	}

	cb := circuitbreaker.NewCircuitBreaker("test", config, logger, registry)

	t.Run("Initially Closed", func(t *testing.T) {
		assert.True(t, cb.AllowRequest())
		assert.Equal(t, circuitbreaker.StateClosed, cb.GetState())
	})

	t.Run("Opens After Failures", func(t *testing.T) {
		// Record two failures
		err := cb.Execute(func() error {
			return errors.New("error 1")
		})
		assert.Error(t, err)
		assert.True(t, cb.AllowRequest(), "Should still allow requests after one failure")
		
		err = cb.Execute(func() error {
			return errors.New("error 2")
		})
		assert.Error(t, err)
		assert.False(t, cb.AllowRequest(), "Should not allow requests after threshold reached")
		assert.Equal(t, circuitbreaker.StateOpen, cb.GetState())
	})

	t.Run("Transitions to Half-Open", func(t *testing.T) {
		// Wait for reset timeout
		time.Sleep(2 * time.Second)
		
		assert.True(t, cb.AllowRequest(), "Should allow one request in half-open state")
		assert.Equal(t, circuitbreaker.StateHalfOpen, cb.GetState())
	})

	t.Run("Closes After Success", func(t *testing.T) {
		err := cb.Execute(func() error {
			return nil
		})
		assert.NoError(t, err)
		assert.True(t, cb.AllowRequest())
		assert.Equal(t, circuitbreaker.StateClosed, cb.GetState())
	})
}

func TestProviderManagerWithCircuitBreaker(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	registry := prometheus.NewRegistry()

	// Create test configuration
	cfg := &config.Config{}
	cfg.Providers = map[string]config.ProviderConfig{
		"primary": {
			Type:  "openai",
			Model: "gpt-3.5-turbo",
		},
		"backup": {
			Type:  "openai",
			Model: "gpt-3.5-turbo",
		},
	}
	cfg.ProviderPreference = []string{"primary", "backup"}
	cfg.CircuitBreaker = config.CircuitBreakerConfig{
		ResetTimeout: 1 * time.Second, // Short timeout for testing
	}

	var primaryCallCount, backupCallCount int

	// Create mock providers
	primaryProvider := mocks.NewMockLLMWithConfig("primary", "gpt-3.5-turbo", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		primaryCallCount++
		return "primary response", nil
	})

	backupProvider := mocks.NewMockLLMWithConfig("backup", "gpt-3.5-turbo", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		backupCallCount++
		return "backup response", nil
	})

	providers := map[string]gollm.LLM{
		"primary": primaryProvider,
		"backup":  backupProvider,
	}

	manager, err := provider.NewManager(cfg, logger, registry)
	require.NoError(t, err)
	require.NotNil(t, manager)

	// Replace providers with mocks
	manager.SetProviders(providers)

	t.Run("Uses Primary Provider", func(t *testing.T) {
		err := manager.Execute(context.Background(), func(llm gollm.LLM) error {
			assert.Equal(t, primaryProvider, llm)
			prompt := &gollm.Prompt{
				Messages: []gollm.PromptMessage{
					{Role: "user", Content: "test"},
				},
			}
			_, err := llm.Generate(context.Background(), prompt)
			return err
		})
		require.NoError(t, err)
		assert.Equal(t, 1, primaryCallCount)
		assert.Equal(t, 0, backupCallCount)
	})

	t.Run("Fails Over to Backup", func(t *testing.T) {
		// Make primary provider fail
		primaryProvider.GenerateFunc = func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
			primaryCallCount++
			return "", errors.New("mock error")
		}

		// Execute enough requests to trip the circuit breaker
		for i := 0; i < 3; i++ {
			manager.Execute(context.Background(), func(llm gollm.LLM) error {
				prompt := &gollm.Prompt{
					Messages: []gollm.PromptMessage{
						{Role: "user", Content: "test"},
					},
				}
				_, err := llm.Generate(context.Background(), prompt)
				return err
			})
		}

		// Next request should use backup provider
		err := manager.Execute(context.Background(), func(llm gollm.LLM) error {
			assert.Equal(t, backupProvider, llm)
			prompt := &gollm.Prompt{
				Messages: []gollm.PromptMessage{
					{Role: "user", Content: "test"},
				},
			}
			_, err := llm.Generate(context.Background(), prompt)
			return err
		})
		require.NoError(t, err)
		assert.True(t, backupCallCount > 0)
	})

	t.Run("Recovers Primary Provider", func(t *testing.T) {
		// Wait for circuit breaker timeout
		time.Sleep(2 * time.Second)

		// Fix primary provider
		primaryProvider.GenerateFunc = func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
			primaryCallCount++
			return "primary response", nil
		}

		// Should eventually switch back to primary
		err := manager.Execute(context.Background(), func(llm gollm.LLM) error {
			assert.Equal(t, primaryProvider, llm)
			prompt := &gollm.Prompt{
				Messages: []gollm.PromptMessage{
					{Role: "user", Content: "test"},
				},
			}
			_, err := llm.Generate(context.Background(), prompt)
			return err
		})
		require.NoError(t, err)
	})
}

func TestProviderManagerAllProvidersFailing(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	registry := prometheus.NewRegistry()

	cfg := &config.Config{}
	cfg.Providers = map[string]config.ProviderConfig{
		"primary": {Type: "openai", Model: "gpt-3.5-turbo"},
		"backup":  {Type: "openai", Model: "gpt-3.5-turbo"},
	}
	cfg.ProviderPreference = []string{"primary", "backup"}

	// Create failing providers
	primaryProvider := mocks.NewMockLLMWithConfig("primary", "gpt-3.5-turbo", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "", errors.New("mock error")
	})

	backupProvider := mocks.NewMockLLMWithConfig("backup", "gpt-3.5-turbo", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "", errors.New("mock error")
	})

	providers := map[string]gollm.LLM{
		"primary": primaryProvider,
		"backup":  backupProvider,
	}

	manager, err := provider.NewManager(cfg, logger, registry)
	require.NoError(t, err)
	require.NotNil(t, manager)

	// Replace providers with mocks
	manager.SetProviders(providers)

	// Trip circuit breakers for both providers
	for i := 0; i < 5; i++ {
		err := manager.Execute(context.Background(), func(llm gollm.LLM) error {
			prompt := &gollm.Prompt{
				Messages: []gollm.PromptMessage{
					{Role: "user", Content: "test"},
				},
			}
			_, err := llm.Generate(context.Background(), prompt)
			return err
		})
		assert.Error(t, err)
	}

	// Should return ErrNoHealthyProvider
	err = manager.Execute(context.Background(), func(llm gollm.LLM) error {
		prompt := &gollm.Prompt{
			Messages: []gollm.PromptMessage{
				{Role: "user", Content: "test"},
			},
		}
		_, err := llm.Generate(context.Background(), prompt)
		return err
	})
	assert.ErrorIs(t, err, provider.ErrNoHealthyProvider)
}
