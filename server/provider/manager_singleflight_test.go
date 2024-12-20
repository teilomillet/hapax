package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/mocks"
	"go.uber.org/zap"
)

func getTestConfig() *config.Config {
	return &config.Config{
		TestMode: true,
		CircuitBreaker: config.CircuitBreakerConfig{
			ResetTimeout: time.Second * 30,
		},
		ProviderPreference: []string{"test-provider"}, // Match the provider name we use
	}
}

// setupTestManager creates a manager and initializes a healthy provider
func setupTestManager(t *testing.T) *Manager {
	logger, _ := zap.NewDevelopment()
	registry := prometheus.NewRegistry()
	m, _ := NewManager(getTestConfig(), logger, registry)
	return m
}

func TestManagerSingleflight(t *testing.T) {
	t.Parallel() // Allow parallel execution with other tests

	tests := []struct {
		name      string
		scenario  func(*testing.T, *Manager)
	}{
		{
			name: "Concurrent identical requests are deduplicated",
			scenario: func(t *testing.T, m *Manager) {
				callCount := 0
				mock := mocks.NewMockLLMWithConfig("test-provider", "test-model", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
					callCount++
					time.Sleep(100 * time.Millisecond) // Simulate work
					return "mock response", nil
				})
				m.SetProviders(map[string]gollm.LLM{"test-provider": mock})

				// Set initial health status
				m.UpdateHealthStatus("test-provider", HealthStatus{
					Healthy:    true,
					LastCheck:  time.Now(),
					Latency:    time.Millisecond,
					ErrorCount: 0,
				})

				// Create a context with timeout
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				// Launch 10 concurrent identical requests
				var wg sync.WaitGroup
				errs := make([]error, 10)
				prompt := mock.NewPrompt("test")
				for i := 0; i < 10; i++ {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						errs[idx] = m.Execute(ctx, func(llm gollm.LLM) error {
							_, err := llm.Generate(ctx, prompt)
							return err
						}, prompt)
					}(i)
				}

				// Add a timeout to WaitGroup
				done := make(chan struct{})
				go func() {
					wg.Wait()
					close(done)
				}()

				select {
				case <-done:
					// Success path - continue with verification
				case <-time.After(2 * time.Second):
					t.Fatal("Test timed out waiting for concurrent requests")
				}

				// Verify all requests succeeded
				for _, err := range errs {
					assert.NoError(t, err)
				}

				// Verify the provider was only called once
				assert.Equal(t, 1, callCount)

				// Verify deduplicated requests metric
				count, err := getCounterValue(m.deduplicatedRequests)
				assert.NoError(t, err)
				assert.Equal(t, float64(9), count) // 9 requests were deduplicated (all but the first)
			},
		},
		{
			name: "Different requests are not deduplicated",
			scenario: func(t *testing.T, m *Manager) {
				callCount := 0
				mock := mocks.NewMockLLMWithConfig("test-provider", "test-model", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
					callCount++
					time.Sleep(50 * time.Millisecond) // Simulate work
					return "mock response", nil
				})
				m.SetProviders(map[string]gollm.LLM{"test-provider": mock})

				// Set initial health status
				m.UpdateHealthStatus("test-provider", HealthStatus{
					Healthy:    true,
					LastCheck:  time.Now(),
					Latency:    time.Millisecond,
					ErrorCount: 0,
				})

				// Create a context with timeout
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				// Launch concurrent different requests
				var wg sync.WaitGroup
				for i := 0; i < 5; i++ {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						prompt := mock.NewPrompt(fmt.Sprintf("test-%d", idx))
						_ = m.Execute(ctx, func(llm gollm.LLM) error {
							// Each request is unique due to different prompts
							_, err := llm.Generate(ctx, prompt)
							return err
						}, prompt)
					}(i)
				}

				// Add a timeout to WaitGroup
				done := make(chan struct{})
				go func() {
					wg.Wait()
					close(done)
				}()

				select {
				case <-done:
					// Success path - continue with verification
				case <-time.After(2 * time.Second):
					t.Fatal("Test timed out waiting for concurrent requests")
				}

				// Verify the provider was called for each unique request
				assert.Equal(t, 5, callCount)
			},
		},
		{
			name: "Circuit breaker integration",
			scenario: func(t *testing.T, m *Manager) {
				mock := mocks.NewMockLLMWithConfig("test-provider", "test-model", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
					return "", errors.New("simulated failure")
				})
				m.SetProviders(map[string]gollm.LLM{"test-provider": mock})

				// Set initial health status
				m.UpdateHealthStatus("test-provider", HealthStatus{
					Healthy:    true,
					LastCheck:  time.Now(),
					Latency:    time.Millisecond,
					ErrorCount: 0,
				})

				// Create a context with timeout
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				// Make concurrent requests to trigger circuit breaker
				var wg sync.WaitGroup
				errs := make([]error, 3)
				prompt := mock.NewPrompt("test")
				for i := 0; i < 3; i++ {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						errs[idx] = m.Execute(ctx, func(llm gollm.LLM) error {
							_, err := llm.Generate(ctx, prompt)
							return err
						}, prompt)
					}(i)
				}

				// Add a timeout to WaitGroup
				done := make(chan struct{})
				go func() {
					wg.Wait()
					close(done)
				}()

				select {
				case <-done:
					// Success path - continue with verification
				case <-time.After(2 * time.Second):
					t.Fatal("Test timed out waiting for concurrent requests")
				}

				// Verify all errors contain the expected message
				for _, err := range errs {
					assert.Contains(t, err.Error(), "no healthy provider available")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // Allow parallel execution of subtests
			m := setupTestManager(t)
			tt.scenario(t, m)
		})
	}
}

// Helper function to get counter value
func getCounterValue(counter prometheus.Counter) (float64, error) {
	metricChan := make(chan prometheus.Metric, 1)
	counter.Collect(metricChan)
	m := <-metricChan

	// Use dto.Metric instead of prometheus.Metric
	var dtoMetric dto.Metric
	if err := m.Write(&dtoMetric); err != nil {
		return 0, err
	}
	return *dtoMetric.Counter.Value, nil
}
