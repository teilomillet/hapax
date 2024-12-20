package provider

import (
	"context"
	"testing"
	"time"

	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/mocks"
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
				LLM: config.LLMConfig{
					Provider: "mock",
					Model:    "test-model",
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
			manager := NewManagerWithProviders(cfg, logger, providers)

			// Check provider health
			status := manager.checkProviderHealth("mock", mockLLM)

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
		LLM: config.LLMConfig{
			Provider: "primary",
			Model:    "test-model",
			HealthCheck: &config.ProviderHealthCheck{
				Enabled:          true,
				Interval:         100 * time.Millisecond,
				Timeout:         1 * time.Second,
				FailureThreshold: 3,
			},
			BackupProviders: []config.BackupProvider{
				{
					Provider: "backup1",
					Model:    "backup-model",
				},
			},
		},
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
	manager := NewManagerWithProviders(cfg, logger, providers)

	// Update health states
	manager.updateHealthStatus("primary", HealthStatus{Healthy: false, ConsecutiveFails: 3})
	manager.updateHealthStatus("backup1", HealthStatus{Healthy: true, ConsecutiveFails: 0})

	// Test failover
	provider, err := manager.GetHealthyProvider()
	if err != nil {
		t.Fatalf("Failed to get healthy provider: %v", err)
	}

	// Verify we got the backup provider
	if provider.GetProvider() != "backup1" {
		t.Errorf("Expected backup1 provider, got %s", provider.GetProvider())
	}
}
