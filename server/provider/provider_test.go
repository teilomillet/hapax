package provider_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/mocks"
	"github.com/teilomillet/hapax/server/provider"
	"go.uber.org/zap"
)

func TestProviderHealth(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()

	// Define our test cases with explicit expectations
	tests := []struct {
		name          string
		generateFunc  func(context.Context, *gollm.Prompt) (string, error)
		expectHealthy bool
		failureCount  int
		expectedErr   error  // Adding explicit error expectations
		description   string // Adding descriptions helps document test intent
	}{
		{
			name: "healthy_provider",
			generateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
				return "ok", nil
			},
			expectHealthy: true,
			failureCount:  0,
			expectedErr:   nil,
			description:   "Provider should be healthy when generates successfully",
		},
		{
			name: "provider_timeout",
			generateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
				return "", context.DeadlineExceeded
			},
			expectHealthy: false,
			failureCount:  1,
			expectedErr:   context.DeadlineExceeded,
			description:   "Provider should be unhealthy when timing out",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create mock LLM with our test-specific behavior
			mockLLM := mocks.NewMockLLM(tt.generateFunc)

			// Set up context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			// Configure the manager
			cfg := &config.Config{
				TestMode: true,
				CircuitBreaker: config.CircuitBreakerConfig{
					MaxRequests:      1,
					Interval:         100 * time.Millisecond,
					Timeout:          100 * time.Millisecond,
					FailureThreshold: 2,
					TestMode:         true,
				},
			}

			manager, err := provider.NewManager(cfg, logger, prometheus.NewRegistry())
			if err != nil {
				t.Fatalf("Failed to create manager: %v", err)
			}

			// Create test prompt
			prompt := &gollm.Prompt{
				Messages: []gollm.PromptMessage{
					{Role: "user", Content: "test"},
				},
			}

			// Attempt generation and verify error matches expectations
			_, err = mockLLM.Generate(ctx, prompt)
			if !errors.Is(err, tt.expectedErr) {
				t.Errorf("Generate() error = %v, want %v", err, tt.expectedErr)
			}

			// Check provider health status
			status := manager.CheckProviderHealth("test", mockLLM)

			// Verify health status matches expectations
			if status.Healthy != tt.expectHealthy {
				t.Errorf("Health status = %v, want %v", status.Healthy, tt.expectHealthy)
			}

			// Verify failure count matches expectations
			if status.ConsecutiveFails != tt.failureCount {
				t.Errorf("Failure count = %d, want %d", status.ConsecutiveFails, tt.failureCount)
			}
		})
	}
}

// TestProviderFailover verifies the behavior of the circuit breaker
// and the handling of failures across requests.
// It ensures that the first request fails as expected, the second request
// triggers the circuit breaker and attempts to use backup providers,
// and that proper error messages are returned when no providers are available.

func TestProviderFailover(t *testing.T) {
	// This test case is crucial in verifying the circuit breaker behavior
	// and its impact on the overall system reliability.

	logger, _ := zap.NewDevelopment()
	logger.Info("Starting TestProviderFailover")

	// Setup test cases for primary and backup providers
	// Simple config with just two providers
	cfg := &config.Config{
		TestMode: true,
		Providers: map[string]config.ProviderConfig{
			"primary": {Type: "primary", Model: "test"},
			"backup":  {Type: "backup", Model: "test"},
		},
		ProviderPreference: []string{"primary", "backup"},
		CircuitBreaker: config.CircuitBreakerConfig{
			MaxRequests:      1,
			Interval:         time.Second,
			Timeout:          time.Second,
			FailureThreshold: 2,
			TestMode:         true,
		},
	}

	logger.Info("Creating providers")
	callCount := 0
	// Create providers with logging
	primaryProvider := mocks.NewMockLLMWithConfig("primary", "test", func(ctx context.Context, p *gollm.Prompt) (string, error) {
		callCount++
		logger.Info("Primary provider called", zap.Int("call_count", callCount))
		return "", fmt.Errorf("primary error")
	})

	backupProvider := mocks.NewMockLLMWithConfig("backup", "test", func(ctx context.Context, p *gollm.Prompt) (string, error) {
		logger.Info("Backup provider called")
		return "backup ok", nil
	})

	providers := map[string]gollm.LLM{
		"primary": primaryProvider,
		"backup":  backupProvider,
	}

	logger.Info("Creating manager")
	reg := prometheus.NewRegistry()
	manager, err := provider.NewManager(cfg, logger, reg)
	require.NoError(t, err)

	logger.Info("Setting providers")
	manager.SetProviders(providers)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	prompt := &gollm.Prompt{
		Messages: []gollm.PromptMessage{{Role: "user", Content: "test"}},
	}

	// **Key Insights**
	//
	// The key insight is to understand the relationship between single-request behavior and cross-request state.
	// The circuit breaker maintains state across requests, but each individual request needs clear, predictable behavior.
	//
	// **Single-Request Behavior**
	//
	// When the first request comes in:
	// 1. The breaker is closed (not open).
	// 2. We hit the else clause.
	// 3. We return the primary error immediately.
	// 4. This failure gets recorded in the circuit breaker's state.
	//
	// **Cross-Request State Evolution**
	//
	// For the second request:
	// 1. The primary provider fails again.
	// 2. This triggers the circuit breaker to open.
	// 3. Because the breaker is now open, we hit the first condition.
	// 4. The continue statement moves us to try the backup provider.
	// 5. All of this happens within the same request.
	//
	// **Properties Maintained**
	//
	// This pattern maintains two important properties:
	// 1. **Isolation**: Each request has clear, predictable behavior.
	// 2. **State Evolution**: The circuit breaker accumulates state across requests.

	// First request should fail with the primary error
	// This verifies that the circuit breaker records the failure correctly.
	logger.Info("Executing first request (should fail)")
	err = manager.Execute(ctx, func(llm gollm.LLM) error {
		result, err := llm.Generate(ctx, prompt)
		logger.Info("First request result", zap.String("result", result), zap.Error(err))
		return err
	}, prompt)
	require.Error(t, err)
	logger.Info("First request completed", zap.Error(err))

	// Second request should try primary, detect circuit breaker opening,
	// and immediately try backup providers.
	// If all providers fail, we need to ensure proper error handling
	// and meaningful error messages are returned.
	logger.Info("Executing second request (should succeed using backup)")
	err = manager.Execute(ctx, func(llm gollm.LLM) error {
		result, err := llm.Generate(ctx, prompt)
		logger.Info("Second request result", zap.String("result", result), zap.Error(err))
		if err == nil {
			require.Equal(t, "backup ok", result)
		}
		return err
	}, prompt)
	require.NoError(t, err)
	logger.Info("Second request completed", zap.Error(err))
}

func TestMetricsInProduction(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	logger := zap.NewNop()
	registry := prometheus.NewRegistry()

	cfg := &config.Config{
		TestMode: true,
		Providers: map[string]config.ProviderConfig{
			"test": {
				Type:  "test",
				Model: "test-model",
			},
		},
		ProviderPreference: []string{"test"},
		CircuitBreaker: config.CircuitBreakerConfig{
			MaxRequests:      1,
			Interval:         time.Second,
			Timeout:          100 * time.Millisecond,
			FailureThreshold: 2,
			TestMode:         true,
		},
	}

	mockLLM := mocks.NewMockLLMWithConfig("test", "test-model", func(ctx context.Context, p *gollm.Prompt) (string, error) {
		return "ok", nil
	})

	providers := map[string]gollm.LLM{
		"test": mockLLM,
	}

	manager, err := provider.NewManager(cfg, logger, registry)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	manager.SetProviders(providers)

	prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{{Role: "user", Content: "test"}}}
	_, err = mockLLM.Generate(ctx, prompt)
	if err != nil {
		t.Fatalf("Failed to generate response: %v", err)
	}

	manager.UpdateHealthStatus("test", provider.HealthStatus{
		Healthy:    true,
		LastCheck:  time.Now(),
		ErrorCount: 0,
	})

	healthCheckErrors := manager.GetHealthCheckErrors()

	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	var healthCheckErrorsValue float64
	for _, mf := range metricFamilies {
		if mf.GetName() == "provider_health_check_errors_total" {
			for _, m := range mf.GetMetric() {
				if m.Counter != nil {
					healthCheckErrorsValue = m.Counter.GetValue()
					break
				}
			}
		}
	}

	if healthCheckErrorsValue != 0 {
		t.Errorf("Expected 0 health check errors, got %v", healthCheckErrorsValue)
	}

	healthCheckErrors.WithLabelValues("test").Inc()

	value := testutil.ToFloat64(healthCheckErrors.WithLabelValues("test"))
	if value != 1 {
		t.Errorf("Expected 1 health check error, got %v", value)
	}
}

func TestMultiProviderSeamlessConnection(t *testing.T) {
	// Create mock providers with different behaviors
	openaiProvider := mocks.NewMockLLMWithConfig("openai", "gpt-3.5", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "OpenAI response", nil
	})

	anthropicProvider := mocks.NewMockLLMWithConfig("anthropic", "claude-3-haiku", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "Anthropic response", nil
	})

	// Configuration with multiple providers and preferences
	cfg := &config.Config{
		TestMode: true,
		Providers: map[string]config.ProviderConfig{
			"openai": {
				Type:  "openai",
				Model: "gpt-3.5-turbo",
			},
			"anthropic": {
				Type:  "anthropic",
				Model: "claude-3-haiku",
			},
		},
		ProviderPreference: []string{"openai", "anthropic"},
	}

	// Create logger and metrics registry
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()

	// Create provider manager
	manager, err := provider.NewManager(cfg, logger, registry)
	require.NoError(t, err)

	// Set up providers
	providers := map[string]gollm.LLM{
		"openai":    openaiProvider,
		"anthropic": anthropicProvider,
	}
	manager.SetProviders(providers)

	// Create test prompt
	prompt := &gollm.Prompt{
		Messages: []gollm.PromptMessage{
			{Role: "user", Content: "Test multi-provider connection"},
		},
	}

	// Test primary provider (OpenAI)
	var response string
	err = manager.Execute(context.Background(), func(llm gollm.LLM) error {
		resp, err := llm.Generate(context.Background(), prompt)
		response = resp
		return err
	}, prompt)

	// Assert first call uses OpenAI successfully
	require.NoError(t, err)
	assert.Equal(t, "OpenAI response", response)

	// Simulate OpenAI failure
	openaiProvider.GenerateFunc = func(ctx context.Context, p *gollm.Prompt) (string, error) {
		return "", fmt.Errorf("OpenAI provider failed")
	}

	// Update health status to trigger failover
	manager.UpdateHealthStatus("openai", provider.HealthStatus{
		Healthy:    false,
		LastCheck:  time.Now(),
		ErrorCount: 3,
	})

	// Test failover to Anthropic
	err = manager.Execute(context.Background(), func(llm gollm.LLM) error {
		resp, err := llm.Generate(context.Background(), prompt)
		response = resp
		return err
	}, prompt)

	// Assert failover works and Anthropic is used
	require.NoError(t, err)
	assert.Equal(t, "Anthropic response", response)
}
