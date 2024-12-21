package provider_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

	tests := []struct {
		name          string
		generateFunc  func(context.Context, *gollm.Prompt) (string, error)
		expectHealthy bool
		failureCount  int
	}{
		{
			name: "healthy_provider",
			generateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
				return "ok", nil
			},
			expectHealthy: true,
			failureCount:  0,
		},
		{
			name: "provider_timeout",
			generateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
				// Return timeout error immediately instead of sleeping
				return "", context.DeadlineExceeded
			},
			expectHealthy: false,
			failureCount:  1,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockLLM := mocks.NewMockLLM(tt.generateFunc)

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

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

			prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{{Role: "user", Content: "test"}}}
			_, err = mockLLM.Generate(ctx, prompt)
			status := manager.CheckProviderHealth("test", mockLLM)

			if status.Healthy != tt.expectHealthy {
				t.Errorf("Expected healthy=%v, got %v", tt.expectHealthy, status.Healthy)
			}
			if status.ConsecutiveFails != tt.failureCount {
				t.Errorf("Expected failureCount=%d, got %d", tt.failureCount, status.ConsecutiveFails)
			}
		})
	}
}

func TestProviderFailover(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	logger.Info("Starting TestProviderFailover")

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

	logger.Info("Executing first request (should fail)")
	err = manager.Execute(ctx, func(llm gollm.LLM) error {
		result, err := llm.Generate(ctx, prompt)
		logger.Info("First request result", zap.String("result", result), zap.Error(err))
		return err
	}, prompt)
	require.Error(t, err)
	logger.Info("First request completed", zap.Error(err))

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
