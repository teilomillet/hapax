package provider_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/mocks"
	"github.com/teilomillet/hapax/server/provider"
	"go.uber.org/zap"
)

func TestProviderHealth(t *testing.T) {
	t.Parallel() // Allow parallel execution

	// Setup logger
	logger := zap.NewNop()

	tests := []struct {
		name          string
		generateFunc  func(context.Context, *gollm.Prompt) (string, error)
		expectHealthy bool
		failureCount  int
		timeout       bool
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
				time.Sleep(2 * time.Second)
				return "", context.DeadlineExceeded
			},
			expectHealthy: false,
			failureCount:  1,
			timeout:       true,
		},
	}

	for _, tt := range tests {
		tt := tt // Capture range variable for parallel execution
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // Allow parallel execution of subtests

			// Create mock LLM with shorter timeout for test
			mockLLM := mocks.NewMockLLM(tt.generateFunc)

			// Create a context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			// Create manager with test config
			cfg := &config.Config{
				TestMode: true,
				CircuitBreaker: config.CircuitBreakerConfig{
					ResetTimeout: 100 * time.Millisecond,
				},
			}
			manager, err := provider.NewManager(cfg, logger, prometheus.NewRegistry())
			if err != nil {
				t.Fatalf("Failed to create manager: %v", err)
			}

			// Check provider health with context
			prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{{Role: "user", Content: "test"}}}
			_, err = mockLLM.Generate(ctx, prompt)
			status := manager.CheckProviderHealth("test", mockLLM)

			// Verify health status
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
	t.Parallel() // Allow parallel execution

	// Create a context with timeout for the entire test
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	logger := zap.NewNop()

	// Create config with primary and backup providers
	cfg := &config.Config{
		TestMode: true, // Skip provider initialization
		Providers: map[string]config.ProviderConfig{
			"primary": {
				Type:  "primary",
				Model: "primary-model",
			},
			"backup1": {
				Type:  "backup1",
				Model: "backup-model",
			},
		},
		ProviderPreference: []string{"primary", "backup1"},
		CircuitBreaker: config.CircuitBreakerConfig{
			ResetTimeout: 100 * time.Millisecond,
		},
	}

	// Setup mock providers with context
	primaryLLM := mocks.NewMockLLMWithConfig("primary", "primary-model", func(ctx context.Context, p *gollm.Prompt) (string, error) {
		return "", context.DeadlineExceeded // Primary always fails
	})
	backupLLM := mocks.NewMockLLMWithConfig("backup1", "backup-model", func(ctx context.Context, p *gollm.Prompt) (string, error) {
		return "ok", nil // Backup is healthy
	})

	providers := map[string]gollm.LLM{
		"primary": primaryLLM,
		"backup1": backupLLM,
	}

	// Create manager with mocks
	manager, err := provider.NewManager(cfg, logger, prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	manager.SetProviders(providers)

	// Update health states
	prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{{Role: "user", Content: "test"}}}
	_, err = primaryLLM.Generate(ctx, prompt)
	manager.UpdateHealthStatus("primary", provider.HealthStatus{Healthy: false, ConsecutiveFails: 3})
	manager.UpdateHealthStatus("backup1", provider.HealthStatus{Healthy: true, ConsecutiveFails: 0})

	// Test failover
	provider, err := manager.GetProvider()
	if err != nil {
		t.Fatalf("Failed to get healthy provider: %v", err)
	}

	// Verify we got the backup provider
	if provider != backupLLM {
		t.Errorf("Expected backup1 provider, got %v", provider)
	}
}

func TestMetricsInProduction(t *testing.T) {
	t.Parallel() // Allow parallel execution

	// Create a context with timeout for the entire test
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	logger := zap.NewNop()
	registry := prometheus.NewRegistry()

	// Create test configuration
	cfg := &config.Config{
		TestMode: true, // Test production mode
		Providers: map[string]config.ProviderConfig{
			"test": {
				Type:  "test",
				Model: "test-model",
			},
		},
		ProviderPreference: []string{"test"},
		CircuitBreaker: config.CircuitBreakerConfig{
			ResetTimeout: 100 * time.Millisecond,
		},
	}

	// Create mock provider with context
	mockLLM := mocks.NewMockLLMWithConfig("test", "test-model", func(ctx context.Context, p *gollm.Prompt) (string, error) {
		return "ok", nil
	})

	providers := map[string]gollm.LLM{
		"test": mockLLM,
	}

	// Create manager with mocks
	manager, err := provider.NewManager(cfg, logger, registry)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	manager.SetProviders(providers)

	// Test provider with context
	prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{{Role: "user", Content: "test"}}}
	_, err = mockLLM.Generate(ctx, prompt)
	if err != nil {
		t.Fatalf("Failed to generate response: %v", err)
	}

	// Update health status
	manager.UpdateHealthStatus("test", provider.HealthStatus{
		Healthy:    true,
		LastCheck:  time.Now(),
		ErrorCount: 0,
	})

	// Get metrics
	healthCheckErrors := manager.GetHealthCheckErrors()

	// Verify initial state
	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Check health check errors metric
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

	// Verify initial state
	if healthCheckErrorsValue != 0 {
		t.Errorf("Expected 0 health check errors, got %v", healthCheckErrorsValue)
	}

	// Simulate a health check error
	healthCheckErrors.WithLabelValues("test", "timeout").Inc()

	// Verify updated state
	value := testutil.ToFloat64(healthCheckErrors.WithLabelValues("test", "timeout"))
	if value != 1 {
		t.Errorf("Expected 1 health check error, got %v", value)
	}
}
