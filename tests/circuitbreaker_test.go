package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sony/gobreaker"
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
	logger, _ := zap.NewDevelopment()
	registry := prometheus.NewRegistry()

	// Helper function to create a new circuit breaker for each test
	newCB := func() (*circuitbreaker.CircuitBreaker, error) {
		return circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
			Name:             "test",
			MaxRequests:      1,
			Interval:         time.Second,
			Timeout:          100 * time.Millisecond,
			FailureThreshold: 2,
			TestMode:         true,
		}, logger, registry)
	}

	t.Run("Initially Closed", func(t *testing.T) {
		cb, err := newCB()
		require.NoError(t, err)
		assert.Equal(t, gobreaker.StateClosed, cb.State())
	})

	t.Run("Opens After Failures", func(t *testing.T) {
		cb, err := newCB()
		require.NoError(t, err)

		// First failure
		err = cb.Execute(func() error {
			return errors.New("error 1")
		})
		assert.Error(t, err)
		assert.Equal(t, gobreaker.StateClosed, cb.State())

		// Second failure - should trip
		err = cb.Execute(func() error {
			return errors.New("error 2")
		})
		assert.Error(t, err)
		assert.Equal(t, gobreaker.StateOpen, cb.State())

		// Additional requests should fail with circuit breaker error
		err = cb.Execute(func() error {
			return nil
		})
		assert.Error(t, err)
		assert.Equal(t, err.Error(), "circuit breaker is open")
	})

	t.Run("Transitions to Half-Open", func(t *testing.T) {
		cb, err := newCB()
		require.NoError(t, err)

		// Trip the breaker
		for i := 0; i < 2; i++ {
			err := cb.Execute(func() error {
				return errors.New("failure")
			})
			assert.Error(t, err)
		}
		assert.Equal(t, gobreaker.StateOpen, cb.State())

		// Wait for timeout
		time.Sleep(150 * time.Millisecond)

		// Should be in half-open state
		assert.Equal(t, gobreaker.StateHalfOpen, cb.State())

		// Fail the test request
		err = cb.Execute(func() error {
			return errors.New("failure in half-open")
		})
		assert.Error(t, err)
		assert.Equal(t, gobreaker.StateOpen, cb.State())
	})

	t.Run("Closes After Success", func(t *testing.T) {
		cb, err := newCB()
		require.NoError(t, err)

		// Trip the breaker
		for i := 0; i < 2; i++ {
			err := cb.Execute(func() error {
				return errors.New("failure")
			})
			assert.Error(t, err)
		}
		assert.Equal(t, gobreaker.StateOpen, cb.State())

		// Wait for timeout
		time.Sleep(150 * time.Millisecond)
		assert.Equal(t, gobreaker.StateHalfOpen, cb.State())

		// Successful request should close the circuit
		err = cb.Execute(func() error {
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, gobreaker.StateClosed, cb.State())
	})

	t.Run("Maintains Failure Count", func(t *testing.T) {
		cb, err := newCB()
		require.NoError(t, err)

		// Execute a mix of successful and failed requests
		for i := 0; i < 3; i++ {
			cb.Execute(func() error {
				if i%2 == 0 {
					return errors.New("failure")
				}
				return nil
			})
		}

		counts := cb.Counts()
		assert.True(t, counts.TotalFailures > 0)
		assert.True(t, counts.Requests > counts.TotalFailures)
	})
}

func TestProviderManagerWithCircuitBreaker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logger, _ := zap.NewDevelopment()
	registry := prometheus.NewRegistry()

	cfg := &config.Config{
		TestMode: true,
		Providers: map[string]config.ProviderConfig{
			"primary": {Type: "primary", Model: "model"},
			"backup":  {Type: "backup", Model: "model"},
		},
		ProviderPreference: []string{"primary", "backup"},
		CircuitBreaker: config.CircuitBreakerConfig{
			MaxRequests:      1,
			Interval:         10 * time.Millisecond,
			Timeout:          100 * time.Millisecond,
			FailureThreshold: 1, // Change to 1 to make it trip after first failure
			TestMode:         true,
		},
	}

	// Start with primary working
	primaryProvider := mocks.NewMockLLMWithConfig("primary", "model", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "primary response", nil
	})

	backupProvider := mocks.NewMockLLMWithConfig("backup", "model", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "backup response", nil
	})

	providers := map[string]gollm.LLM{
		"primary": primaryProvider,
		"backup":  backupProvider,
	}

	manager, err := provider.NewManager(cfg, logger, registry)
	require.NoError(t, err)
	manager.SetProviders(providers)

	t.Run("Uses Primary Provider", func(t *testing.T) {
		prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{{Role: "user", Content: "test"}}}

		var response string
		err := manager.Execute(ctx, func(llm gollm.LLM) error {
			resp, err := llm.Generate(ctx, prompt)
			response = resp
			return err
		}, prompt)
		require.NoError(t, err)
		assert.Equal(t, "primary response", response)
	})

	t.Run("Fails Over to Backup", func(t *testing.T) {
		// Make primary fail with specific error
		primaryProvider.GenerateFunc = func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
			return "", errors.New("primary error")
		}

		// Use the same role for all requests
		basePrompt := gollm.PromptMessage{Role: "user"}
		prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{
			{Role: basePrompt.Role, Content: "test"},
		}}

		// First request should fail and mark primary as unhealthy
		err := manager.Execute(ctx, func(llm gollm.LLM) error {
			_, err := llm.Generate(ctx, prompt)
			return err
		}, prompt)
		require.Error(t, err, "First request should fail")
		assert.Contains(t, err.Error(), "primary error", "Should be primary provider error")

		// Update primary provider's health status to force failover
		manager.UpdateHealthStatus("primary", provider.HealthStatus{
			Healthy:    false,
			LastCheck:  time.Now(),
			ErrorCount: 3,
		})

		// Next request should use backup
		var response string
		err = manager.Execute(ctx, func(llm gollm.LLM) error {
			resp, err := llm.Generate(ctx, prompt)
			response = resp
			return err
		}, prompt)
		require.NoError(t, err, "Request should succeed with backup")
		assert.Equal(t, "backup response", response, "Should get backup response")
	})

	t.Run("Recovers Primary Provider", func(t *testing.T) {
		time.Sleep(150 * time.Millisecond) // Wait for circuit breaker to reset

		// Make primary work again
		primaryProvider.GenerateFunc = func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
			return "primary response", nil
		}

		// Reset health status
		manager.UpdateHealthStatus("primary", provider.HealthStatus{
			Healthy:    true,
			LastCheck:  time.Now(),
			ErrorCount: 0,
		})

		prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{{Role: "user", Content: "test"}}}

		var response string
		err := manager.Execute(ctx, func(llm gollm.LLM) error {
			resp, err := llm.Generate(ctx, prompt)
			response = resp
			return err
		}, prompt)
		require.NoError(t, err)
		assert.Equal(t, "primary response", response)
	})
}

func TestProviderManagerAllProvidersFailing(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	registry := prometheus.NewRegistry()

	cfg := &config.Config{
		TestMode: true,
		Providers: map[string]config.ProviderConfig{
			"primary": {Type: "primary", Model: "model"},
			"backup":  {Type: "backup", Model: "model"},
		},
		ProviderPreference: []string{"primary", "backup"},
		CircuitBreaker: config.CircuitBreakerConfig{
			MaxRequests:      1,
			Interval:         10 * time.Millisecond,
			Timeout:          10 * time.Millisecond,
			FailureThreshold: 2,
			TestMode:         true,
		},
	}

	// Create providers that would fail if called
	primaryProvider := mocks.NewMockLLMWithConfig("primary", "model", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "", errors.New("primary error")
	})
	backupProvider := mocks.NewMockLLMWithConfig("backup", "model", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "", errors.New("backup error")
	})

	providers := map[string]gollm.LLM{
		"primary": primaryProvider,
		"backup":  backupProvider,
	}

	manager, err := provider.NewManager(cfg, logger, registry)
	require.NoError(t, err)
	manager.SetProviders(providers)

	// Pre-mark both providers as unhealthy
	manager.UpdateHealthStatus("primary", provider.HealthStatus{
		Healthy:    false,
		LastCheck:  time.Now(),
		ErrorCount: 3,
	})
	manager.UpdateHealthStatus("backup", provider.HealthStatus{
		Healthy:    false,
		LastCheck:  time.Now(),
		ErrorCount: 3,
	})

	prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{{Role: "user", Content: "test"}}}

	// The Execute call should fail immediately without trying any providers
	err = manager.Execute(context.Background(), func(llm gollm.LLM) error {
		_, err := llm.Generate(context.Background(), prompt)
		return err
	}, prompt)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no healthy provider available")
}
