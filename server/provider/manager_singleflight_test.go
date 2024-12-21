package provider

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/mocks"
	"go.uber.org/zap"
)

func TestManagerSingleflight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		testFn func(*testing.T, *Manager)
	}{
		{
			name: "Concurrent identical requests are deduplicated",
			testFn: func(t *testing.T, m *Manager) {
				var callCount atomic.Int32
				mock := mocks.NewMockLLMWithConfig("test", "model", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
					callCount.Add(1)
					// Small sleep to ensure concurrent requests overlap
					time.Sleep(10 * time.Millisecond)
					return "response", nil
				})

				m.SetProviders(map[string]gollm.LLM{"test": mock})
				m.UpdateHealthStatus("test", HealthStatus{
					Healthy:    true,
					LastCheck:  time.Now(),
					ErrorCount: 0,
				})

				// Create identical prompts
				prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{{
					Role:    "user",
					Content: "test",
				}}}

				// Launch concurrent requests
				var wg sync.WaitGroup
				errs := make([]error, 5)
				for i := 0; i < 5; i++ {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						errs[idx] = m.Execute(context.Background(), func(llm gollm.LLM) error {
							_, err := llm.Generate(context.Background(), prompt)
							return err
						}, prompt)
					}(i)
				}

				waitWithTimeout(&wg, t, 100*time.Millisecond)

				// Verify results
				for _, err := range errs {
					assert.NoError(t, err)
				}

				// Should only be called once due to deduplication
				assert.Equal(t, int32(1), callCount.Load())
			},
		},
		{
			name: "Different requests are not deduplicated",
			testFn: func(t *testing.T, m *Manager) {
				var callCount atomic.Int32
				mock := mocks.NewMockLLMWithConfig("test", "model", func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
					callCount.Add(1)
					time.Sleep(10 * time.Millisecond)
					return "response", nil
				})

				m.SetProviders(map[string]gollm.LLM{"test": mock})
				m.UpdateHealthStatus("test", HealthStatus{
					Healthy:    true,
					LastCheck:  time.Now(),
					ErrorCount: 0,
				})

				var wg sync.WaitGroup
				for i := 0; i < 3; i++ {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						// Different prompts
						prompt := &gollm.Prompt{Messages: []gollm.PromptMessage{{
							Role:    "user",
							Content: fmt.Sprintf("test-%d", idx),
						}}}
						_ = m.Execute(context.Background(), func(llm gollm.LLM) error {
							_, err := llm.Generate(context.Background(), prompt)
							return err
						}, prompt)
					}(i)
				}

				waitWithTimeout(&wg, t, 100*time.Millisecond)
				assert.Equal(t, int32(3), callCount.Load())
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{
				TestMode: true,
				Providers: map[string]config.ProviderConfig{
					"test": {Type: "test", Model: "model"},
				},
				ProviderPreference: []string{"test"},
				CircuitBreaker: config.CircuitBreakerConfig{
					MaxRequests:      1,
					Interval:         10 * time.Millisecond,
					Timeout:          10 * time.Millisecond,
					FailureThreshold: 2,
					TestMode:         true,
				},
			}

			manager, err := NewManager(cfg, zap.NewNop(), prometheus.NewRegistry())
			require.NoError(t, err)
			tt.testFn(t, manager)
		})
	}
}

// Helper function to wait for WaitGroup with timeout
func waitWithTimeout(wg *sync.WaitGroup, t *testing.T, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success path - continue
	case <-time.After(timeout):
		t.Fatal("Test timed out waiting for concurrent requests")
	}
}
