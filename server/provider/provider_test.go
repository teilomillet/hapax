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
	// Setup logger
	logger := zap.NewNop()

	tests := []struct {
		name           string
		generateFunc   func(context.Context, *gollm.Prompt) (string, error)
		expectHealthy  bool
		failureCount   int
		timeout       bool
	}{
		{
			name: "healthy_provider",
			generateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
				return "ok", nil
			},
			expectHealthy: true,
			failureCount: 0,
		},
		{
			name: "provider_timeout",
			generateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
				time.Sleep(2 * time.Second)
				return "", context.DeadlineExceeded
			},
			expectHealthy: false,
			failureCount: 1,
			timeout:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock LLM
			mockLLM := mocks.NewMockLLM(tt.generateFunc)

			// Create config with health check settings
			cfg := &config.Config{
				TestMode: true,  // Skip provider initialization
				Providers: map[string]config.ProviderConfig{
					"mock": {
						Type:  "mock",
						Model: "test-model",
					},
				},
				ProviderPreference: []string{"mock"},
				LLM: config.LLMConfig{
					HealthCheck: &config.ProviderHealthCheck{
						Enabled:          true,
						Interval:         100 * time.Millisecond,
						Timeout:         1 * time.Second,
						FailureThreshold: 3,
					},
				},
			}

			// Create provider manager with mock
			providers := map[string]gollm.LLM{
				"mock": mockLLM,
			}
			manager, err := provider.NewManager(cfg, logger, prometheus.NewRegistry())
			if err != nil {
				t.Fatalf("Failed to create manager: %v", err)
			}
			manager.SetProviders(providers)

			// Check provider health
			status := manager.CheckProviderHealth("mock", mockLLM)

			// Verify health status
			if status.Healthy != tt.expectHealthy {
				t.Errorf("Expected health status %v, got %v", tt.expectHealthy, status.Healthy)
			}

			if status.ConsecutiveFails != tt.failureCount {
				t.Errorf("Expected failure count %d, got %d", tt.failureCount, status.ConsecutiveFails)
			}

			// Verify latency for timeout cases
			if tt.timeout && status.Latency < time.Second {
				t.Errorf("Expected latency > 1s for timeout case, got %v", status.Latency)
			}
		})
	}
}

func TestProviderFailover(t *testing.T) {
	logger := zap.NewNop()

	// Create config with primary and backup providers
	cfg := &config.Config{
		TestMode: true,  // Skip provider initialization
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
	}

	// Setup mock providers
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
	logger := zap.NewNop()

	// Create config without test mode
	cfg := &config.Config{
		TestMode: true, // Skip provider initialization but not metrics
		Providers: map[string]config.ProviderConfig{
			"mock": {
				Type:  "mock",
				Model: "test-model",
			},
		},
		ProviderPreference: []string{"mock"},
		LLM: config.LLMConfig{
			HealthCheck: &config.ProviderHealthCheck{
				Enabled:          true,
				Interval:         100 * time.Millisecond,
				Timeout:         1 * time.Second,
				FailureThreshold: 3,
			},
		},
	}

	// Create mock provider that fails
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, p *gollm.Prompt) (string, error) {
		return "", context.DeadlineExceeded
	})

	// Create registry and manager
	registry := prometheus.NewRegistry()
	manager, err := provider.NewManager(cfg, logger, registry)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Set providers with metrics enabled
	providers := map[string]gollm.LLM{
		"mock": mockLLM,
	}
	manager.SetProviders(providers)

	// Check provider health (should fail)
	status := manager.CheckProviderHealth("mock", mockLLM)
	if status.Healthy {
		t.Error("Expected provider to be unhealthy")
	}

	// Verify metrics were registered and updated
	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Check if we have the expected metrics
	expectedMetrics := []string{
		"hapax_health_check_duration_seconds",
		"hapax_health_check_errors_total",
		"hapax_circuit_breaker_state",
		"hapax_circuit_breaker_failures_total",
		"hapax_circuit_breaker_trips_total",
	}

	for _, metricName := range expectedMetrics {
		found := false
		for _, mf := range metricFamilies {
			if mf.GetName() == metricName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected metric %s not found", metricName)
		}
	}

	// Verify error counter was incremented
	errorCount := testutil.ToFloat64(manager.GetHealthCheckErrors().WithLabelValues("mock"))
	if errorCount != 1 {
		t.Errorf("Expected 1 error, got %v", errorCount)
	}
}
