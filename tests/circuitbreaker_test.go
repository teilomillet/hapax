package tests

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/circuitbreaker"
	"github.com/teilomillet/hapax/server/mocks"
	"github.com/teilomillet/hapax/server/provider"
	"go.uber.org/zap"
)

func TestCircuitBreaker(t *testing.T) {
	t.Parallel() // Allow parallel execution

	logger, _ := zap.NewDevelopment()
	registry := prometheus.NewRegistry()

	// Helper function to create a new circuit breaker for each test
	newCB := func() *circuitbreaker.CircuitBreaker {
		return circuitbreaker.NewCircuitBreaker("test", circuitbreaker.Config{
			FailureThreshold: 2,
			ResetTimeout:     100 * time.Millisecond,
			HalfOpenRequests: 1,
			TestMode:         true,
		}, logger, registry)
	}

	t.Run("Initially Closed", func(t *testing.T) {
		t.Parallel()
		cb := newCB()
		assert.True(t, cb.AllowRequest(), "Should allow requests when closed")
		assert.Equal(t, circuitbreaker.StateClosed, cb.GetState())
	})

	t.Run("Opens After Failures", func(t *testing.T) {
		t.Parallel()
		cb := newCB()
		// First failure
		err := cb.Execute(func() error {
			return errors.New("error 1")
		})
		assert.Error(t, err)
		assert.True(t, cb.AllowRequest(), "Should still allow requests after one failure")
		assert.Equal(t, circuitbreaker.StateClosed, cb.GetState())

		// Second failure - should trip
		err = cb.Execute(func() error {
			return errors.New("error 2")
		})
		assert.Error(t, err)
		assert.False(t, cb.AllowRequest(), "Should not allow requests after threshold reached")
		assert.Equal(t, circuitbreaker.StateOpen, cb.GetState())
	})

	t.Run("Transitions to Half-Open", func(t *testing.T) {
		t.Helper()

		t.Parallel()
		cb := newCB()
		// Trip the breaker
		for i := 0; i < 2; i++ {
			err := cb.Execute(func() error {
				return errors.New("failure")
			})
			assert.Error(t, err)
		}
		assert.Equal(t, circuitbreaker.StateOpen, cb.GetState())

		// Wait for reset timeout
		time.Sleep(150 * time.Millisecond)
		assert.True(t, cb.AllowRequest(), "Should allow one request in half-open state")
		assert.Equal(t, circuitbreaker.StateHalfOpen, cb.GetState())

		// Fail the request
		err := cb.Execute(func() error {
			return errors.New("failure in half-open")
		})
		assert.Error(t, err)
		assert.False(t, cb.AllowRequest(), "Should not allow requests after failing in half-open")
		assert.Equal(t, circuitbreaker.StateOpen, cb.GetState())
		assert.Equal(t, 1, cb.GetHalfOpenFailures(), "Should have 1 failure in half-open state")

		// Try another request - should be rejected
		err = cb.Execute(func() error {
			return nil
		})
		assert.Error(t, err)
		assert.Equal(t, circuitbreaker.StateOpen, cb.GetState(), "Should stay in open state")
		assert.Equal(t, 1, cb.GetHalfOpenFailures(), "Should still have 1 failure in half-open state")
	})

	t.Run("Closes After Success", func(t *testing.T) {
		cb := newCB()
		// Trip the breaker
		for i := 0; i < 2; i++ {
			err := cb.Execute(func() error {
				return errors.New("failure")
			})
			assert.Error(t, err)
		}
		assert.Equal(t, circuitbreaker.StateOpen, cb.GetState())

		// Wait for reset timeout
		time.Sleep(150 * time.Millisecond)
		// Successful request in half-open state
		err := cb.Execute(func() error {
			return nil
		})
		assert.NoError(t, err)
		assert.True(t, cb.AllowRequest(), "Should allow requests after closing")
		assert.Equal(t, circuitbreaker.StateClosed, cb.GetState())
	})

	t.Run("Stays Open After Failure in Half-Open", func(t *testing.T) {
		cb := newCB()
		// Get to half-open state
		for i := 0; i < 2; i++ {
			err := cb.Execute(func() error {
				return errors.New("failure")
			})
			assert.Error(t, err)
		}
		assert.Equal(t, circuitbreaker.StateOpen, cb.GetState())

		// Wait for reset timeout
		time.Sleep(150 * time.Millisecond)
		assert.True(t, cb.AllowRequest(), "Should allow one request in half-open state")
		assert.Equal(t, circuitbreaker.StateHalfOpen, cb.GetState())

		// Fail the request
		err := cb.Execute(func() error {
			return errors.New("failure in half-open")
		})
		assert.Error(t, err)
		assert.False(t, cb.AllowRequest(), "Should not allow requests after failing in half-open")
		assert.Equal(t, circuitbreaker.StateOpen, cb.GetState())
		assert.Equal(t, 1, cb.GetHalfOpenFailures(), "Should have 1 failure in half-open state")
	})
}

func TestProviderManagerWithCircuitBreaker(t *testing.T) {
	t.Parallel()

	// Create a context with timeout for the entire test
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logger, _ := zap.NewDevelopment()
	registry := prometheus.NewRegistry()

	logger.Info("Starting test setup")

	// Create test configuration with shorter timeouts
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
		ResetTimeout: 5 * time.Millisecond,
		TestMode:     true,
	}

	var primaryCallCount, backupCallCount int32

	// Create mock providers with atomic counters
	primaryProvider := mocks.NewMockLLMWithConfig("primary", "gpt-3.5-turbo", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		count := atomic.AddInt32(&primaryCallCount, 1)
		logger.Info("Primary provider called", zap.Int32("call_count", count))
		return "primary response", nil
	})

	backupProvider := mocks.NewMockLLMWithConfig("backup", "gpt-3.5-turbo", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		count := atomic.AddInt32(&backupCallCount, 1)
		logger.Info("Backup provider called", zap.Int32("call_count", count))
		return "backup response", nil
	})

	providers := map[string]gollm.LLM{
		"primary": primaryProvider,
		"backup":  backupProvider,
	}

	manager, err := provider.NewManager(cfg, logger, registry)
	require.NoError(t, err)
	require.NotNil(t, manager)
	manager.SetProviders(providers)

	t.Run("Uses Primary Provider", func(t *testing.T) {
		t.Parallel()
		subCtx, subCancel := context.WithTimeout(ctx, 1*time.Second)
		defer subCancel()

		logger.Info("Starting primary provider test")
		prompt := &gollm.Prompt{
			Messages: []gollm.PromptMessage{
				{Role: "user", Content: "test"},
			},
		}

		// First request should use primary
		logger.Info("Executing primary provider request")
		err := manager.Execute(subCtx, func(llm gollm.LLM) error {
			assert.Equal(t, primaryProvider, llm)
			_, err := llm.Generate(subCtx, prompt)
			if err != nil {
				logger.Error("Primary provider failed", zap.Error(err))
			}
			return err
		}, prompt)
		require.NoError(t, err)
		assert.Equal(t, int32(1), atomic.LoadInt32(&primaryCallCount))
		logger.Info("Primary provider test completed successfully")
	})

	t.Run("Fails Over to Backup", func(t *testing.T) {
		t.Parallel()
		subCtx, subCancel := context.WithTimeout(ctx, 2*time.Second)
		defer subCancel()

		logger.Info("Starting failover test")
		prompt := &gollm.Prompt{
			Messages: []gollm.PromptMessage{
				{Role: "user", Content: "test"},
			},
		}

		// Make primary provider fail
		primaryProvider.GenerateFunc = func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
			count := atomic.AddInt32(&primaryCallCount, 1)
			logger.Info("Primary provider failing intentionally", zap.Int32("call_count", count))
			return "", errors.New("primary provider error")
		}

		// Trip the circuit breaker
		logger.Info("Attempting to trip circuit breaker")
		for i := 0; i < 3; i++ {
			logger.Info("Circuit breaker trip attempt", zap.Int("attempt", i+1))
			err := manager.Execute(subCtx, func(llm gollm.LLM) error {
				logger.Info("Executing failing request", zap.Int("attempt", i+1))
				_, err := llm.Generate(subCtx, prompt)
				if err != nil {
					logger.Info("Expected failure occurred", zap.Error(err))
				}
				return err
			}, prompt)
			require.Error(t, err)
			logger.Info("Circuit breaker trip attempt completed", zap.Int("attempt", i+1))
		}

		// Next request should use backup provider
		logger.Info("Attempting to use backup provider")
		err := manager.Execute(subCtx, func(llm gollm.LLM) error {
			assert.Equal(t, backupProvider, llm)
			_, err := llm.Generate(subCtx, prompt)
			return err
		}, prompt)
		require.NoError(t, err)
		assert.True(t, atomic.LoadInt32(&backupCallCount) > 0)
		logger.Info("Backup provider test completed")
	})

	t.Run("Recovers Primary Provider", func(t *testing.T) {
		logger.Info("Starting 'Recovers Primary Provider' test")
		// Wait for circuit breaker timeout
		logger.Info("Waiting for circuit breaker timeout")
		time.Sleep(200 * time.Millisecond) // Wait for twice the reset timeout

		// Fix primary provider
		primaryProvider.GenerateFunc = func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
			count := atomic.AddInt32(&primaryCallCount, 1)
			logger.Info("Primary provider recovered", zap.Int32("call_count", count))
			return "primary response", nil
		}

		// Reset health status
		logger.Info("Resetting primary provider health status")
		manager.UpdateHealthStatus("primary", provider.HealthStatus{
			Healthy:    true,
			LastCheck:  time.Now(),
			ErrorCount: 0,
		})

		prompt := &gollm.Prompt{
			Messages: []gollm.PromptMessage{
				{Role: "user", Content: "test"},
			},
		}

		// Should eventually switch back to primary
		logger.Info("Attempting to use primary provider again")
		err := manager.Execute(ctx, func(llm gollm.LLM) error {
			assert.Equal(t, primaryProvider, llm)
			_, err := llm.Generate(ctx, prompt)
			return err
		}, prompt)
		require.NoError(t, err)
		logger.Info("Primary provider recovery test completed")
	})
}

func TestProviderManagerAllProvidersFailing(t *testing.T) {
	t.Parallel()
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	logger, _ := zap.NewDevelopment()
	registry := prometheus.NewRegistry()

	logger.Info("Starting all providers failing test")

	cfg := &config.Config{}
	cfg.Providers = map[string]config.ProviderConfig{
		"primary": {Type: "openai", Model: "gpt-3.5-turbo"},
		"backup":  {Type: "openai", Model: "gpt-3.5-turbo"},
	}
	cfg.ProviderPreference = []string{"primary", "backup"}
	cfg.CircuitBreaker = config.CircuitBreakerConfig{
		ResetTimeout: 5 * time.Millisecond,  // Much shorter timeout
		TestMode:     true,
	}

	var callCount int32

	// Create failing providers
	primaryProvider := mocks.NewMockLLMWithConfig("primary", "gpt-3.5-turbo", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		atomic.AddInt32(&callCount, 1)
		logger.Info("Primary provider failing", zap.Int32("call_count", atomic.LoadInt32(&callCount)))
		return "", errors.New("mock error")
	})

	backupProvider := mocks.NewMockLLMWithConfig("backup", "gpt-3.5-turbo", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		atomic.AddInt32(&callCount, 1)
		logger.Info("Backup provider failing", zap.Int32("call_count", atomic.LoadInt32(&callCount)))
		return "", errors.New("mock error")
	})

	providers := map[string]gollm.LLM{
		"primary": primaryProvider,
		"backup":  backupProvider,
	}

	manager, err := provider.NewManager(cfg, logger, registry)
	require.NoError(t, err)
	require.NotNil(t, manager)
	
	logger.Info("Setting up providers")
	manager.SetProviders(providers)

	logger.Info("Starting to trip circuit breakers")
	// Trip circuit breakers for both providers
	for i := 0; i < 5; i++ {
		prompt := &gollm.Prompt{
			Messages: []gollm.PromptMessage{
				{Role: "user", Content: "test"},
			},
		}
		
		subCtx, subCancel := context.WithTimeout(ctx, 100*time.Millisecond)
		err := manager.Execute(subCtx, func(llm gollm.LLM) error {
			logger.Info("Executing failing request", zap.Int("attempt", i+1))
			_, err := llm.Generate(subCtx, prompt)
			return err
		}, prompt)
		subCancel()
		assert.Error(t, err)
		logger.Info("Circuit breaker trip attempt completed", zap.Int("attempt", i+1))
	}

	logger.Info("Verifying no healthy providers")
	// Should return ErrNoHealthyProvider
	prompt := &gollm.Prompt{
		Messages: []gollm.PromptMessage{
			{Role: "user", Content: "test"},
		},
	}
	
	subCtx, subCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	err = manager.Execute(subCtx, func(llm gollm.LLM) error {
		_, err := llm.Generate(subCtx, prompt)
		return err
	}, prompt)
	subCancel()
	
	assert.Error(t, err)
	assert.ErrorIs(t, err, provider.ErrNoHealthyProvider)
	assert.Contains(t, err.Error(), "no healthy provider available")
	logger.Info("Test completed successfully")
}
