package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/teilomillet/gollm"
	"go.uber.org/zap"
)

// Execute runs an LLM operation with circuit breaker protection and retry limits
func (m *Manager) Execute(ctx context.Context, operation func(llm gollm.LLM) error, prompt *gollm.Prompt) error {
	// Create a consistent key based on the prompt content and role only
	// Don't include anything that would make identical requests unique
	key := fmt.Sprintf("%s-%s", prompt.Messages[0].Content, prompt.Messages[0].Role)
	m.logger.Debug("Starting Execute", zap.String("key", key))

	type result struct {
		err    error
		status HealthStatus
		name   string
	}

	v, err, shared := m.group.Do(key, func() (interface{}, error) {
		var lastErr error
		retryCount := 0

		for retryCount < maxProviderRetries {
			retryCount++
			m.logger.Debug("provider attempt", zap.Int("retry", retryCount))

			// Get provider preference list under read lock
			m.mu.RLock()
			preference := make([]string, len(m.cfg.ProviderPreference))
			copy(preference, m.cfg.ProviderPreference)
			m.mu.RUnlock()

			for _, name := range preference {
				// Check context cancellation
				if err := ctx.Err(); err != nil {
					return &result{err: fmt.Errorf("context cancelled: %w", err)}, nil
				}

				// Get provider and health status under read lock
				m.mu.RLock()
				provider, exists := m.providers[name]
				if !exists {
					m.mu.RUnlock()
					continue
				}
				status := m.GetHealthStatus(name)
				breaker := m.breakers[name]
				m.mu.RUnlock()

				if !status.Healthy || breaker == nil {
					continue
				}

				// Execute with circuit breaker
				err := breaker.Execute(func() error {
					return operation(provider)
				})

				if err != nil {
					lastErr = err
					m.logger.Debug("operation failed",
						zap.String("provider", name),
						zap.Error(err),
						zap.Int("retry", retryCount))

					return &result{
						err: err,
						status: HealthStatus{
							Healthy:    false,
							LastCheck:  time.Now(),
							ErrorCount: status.ErrorCount + 1,
						},
						name: name,
					}, nil
				}

				// Success case
				return &result{
					err: nil,
					status: HealthStatus{
						Healthy:    true,
						LastCheck:  time.Now(),
						ErrorCount: 0,
					},
					name: name,
				}, nil
			}
		}

		if lastErr != nil {
			return &result{err: fmt.Errorf("max retries (%d) exceeded, last error: %w", maxProviderRetries, lastErr)}, nil
		}
		return &result{err: fmt.Errorf("max retries (%d) exceeded, no healthy provider available", maxProviderRetries)}, nil
	})

	if err != nil {
		m.logger.Debug("Execute failed", zap.Error(err))
		return err
	}

	// Update metrics for deduplicated requests
	if shared {
		m.deduplicatedRequests.Inc()
	}

	// Update health status after singleflight completes
	if r, ok := v.(*result); ok && r.name != "" {
		m.UpdateHealthStatus(r.name, r.status)
	}

	return v.(*result).err
}
