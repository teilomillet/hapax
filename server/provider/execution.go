package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/sony/gobreaker"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/server/circuitbreaker" // Added import for custom circuit breaker
	"go.uber.org/zap"
)

// result represents the outcome of an LLM operation
type result struct {
	err    error
	status HealthStatus
	name   string
}

// Execute coordinates provider execution with proper error handling
func (m *Manager) Execute(ctx context.Context, operation func(llm gollm.LLM) error, prompt *gollm.Prompt) error {
	key := m.generateRequestKey(prompt)
	m.logger.Debug("Starting Execute", zap.String("key", key))

	v, err, shared := m.group.Do(key, func() (interface{}, error) {
		return m.executeWithRetries(ctx, operation)
	})

	if err != nil {
		m.logger.Debug("Execute failed", zap.Error(err))
		return err
	}

	m.handleRequestMetrics(shared)
	return m.processResult(v.(*result))
}

func (m *Manager) executeWithRetries(ctx context.Context, operation func(llm gollm.LLM) error) (*result, error) {
	preference := m.getProviderPreference()
	if len(preference) == 0 {
		return &result{
			err: fmt.Errorf("no providers configured"),
		}, fmt.Errorf("no providers configured")
	}

	var lastResult *result

	// Try each provider in sequence
	for _, name := range preference {
		provider, breaker, status := m.getProviderResources(name)
		if provider == nil || breaker == nil || !status.Healthy {
			continue
		}

		// Try the current provider
		currentResult := m.executeOperation(ctx, operation, provider, breaker, status, name)
		lastResult = currentResult

		if currentResult.err == nil {
			// Success case - return immediately
			return currentResult, nil
		}

		// **Key Insight**
		// =================
		//
		// The key insight nderstand the relationship between single-request behavior and cross-request state.
		// The circuit breaker maintains state across requests, but each individual request needs clear, predictable behavior.

		// **Request Flow**
		// ===============
		//
		// When the first request comes in:
		// 1. The breaker is closed (not open).
		// 2. We hit the else clause.
		// 3. We return the primary error immediately.
		// 4. This failure gets recorded in the circuit breaker's state.

		// For the second request:
		// 1. The primary provider fails again.
		// 2. This triggers the circuit breaker to open.
		// 3. Because the breaker is now open, we hit the first condition.
		// 4. The continue statement moves us to try the backup provider.
		// 5. All of this happens within the same request.

		// **Properties Maintained**
		// =======================
		//
		// This pattern maintains two important properties:
		// 1. **Isolation**: Each request has clear, predictable behavior.
		// 2. **State Evolution**: The circuit breaker accumulates state across requests.

		// Circuit Breaker Logic
		if breaker.State() == gobreaker.StateOpen {
			// If the circuit breaker is open, we check if we're at the last provider in the preference list.
			// If we are, we return the primary error immediately.
			if name == preference[len(preference)-1] {
				return currentResult, currentResult.err // This gives us the immediate failure
			}
			// Continue to the next provider if we are not at the last one.
			continue
		} else {
			// If the breaker is closed, we return the primary error immediately.
			return currentResult, currentResult.err // This gives us the immediate failure
		}
	}

	// Error Handling
	// We always maintain a valid result structure to prevent nil pointer dereference.
	if lastResult == nil {
		return &result{
			err: fmt.Errorf("no healthy provider available"),
		}, fmt.Errorf("no healthy provider available")
	}

	return lastResult, lastResult.err
}

// executeOperation handles a single operation attempt with proper resource cleanup
func (m *Manager) executeOperation(
	ctx context.Context,
	operation func(llm gollm.LLM) error,
	provider gollm.LLM,
	breaker *circuitbreaker.CircuitBreaker,
	status HealthStatus,
	name string) *result {

	start := time.Now()

	err := breaker.Execute(func() error {
		// Always check context before executing operation
		if err := ctx.Err(); err != nil {
			return err
		}
		return operation(provider)
	})

	duration := time.Since(start)
	breakerState := breaker.State()
	breakerCounts := breaker.Counts()

	if err != nil {
		m.logger.Debug("operation failed",
			zap.String("provider", name),
			zap.Error(err),
			zap.Duration("duration", duration),
			zap.String("breaker_state", breakerState.String()),
			zap.Uint32("consecutive_failures", breakerCounts.ConsecutiveFailures))

		return &result{
			err: err,
			status: HealthStatus{
				Healthy:          false,
				LastCheck:        time.Now(),
				ErrorCount:       status.ErrorCount + 1,
				ConsecutiveFails: int(breakerCounts.ConsecutiveFailures),
				Latency:          duration,
				RequestCount:     status.RequestCount + 1,
			},
			name: name,
		}
	}

	return &result{
		err: nil,
		status: HealthStatus{
			Healthy:          true,
			LastCheck:        time.Now(),
			ErrorCount:       0,
			ConsecutiveFails: 0,
			Latency:          duration,
			RequestCount:     status.RequestCount + 1,
		},
		name: name,
	}
}

// generateRequestKey creates a consistent key based on the prompt content and role
func (m *Manager) generateRequestKey(prompt *gollm.Prompt) string {
	return fmt.Sprintf("%s-%s", prompt.Messages[0].Content, prompt.Messages[0].Role)
}

// getProviderPreference safely retrieves the current provider preference list
func (m *Manager) getProviderPreference() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	preference := make([]string, len(m.cfg.ProviderPreference))
	copy(preference, m.cfg.ProviderPreference)
	return preference
}

// getProviderResources safely retrieves provider-related resources
func (m *Manager) getProviderResources(name string) (gollm.LLM, *circuitbreaker.CircuitBreaker, HealthStatus) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	provider, exists := m.providers[name]
	if !exists {
		return nil, nil, HealthStatus{}
	}

	return provider, m.breakers[name], m.GetHealthStatus(name)
}

// handleRequestMetrics updates metrics for deduplicated requests
func (m *Manager) handleRequestMetrics(shared bool) {
	if shared {
		m.deduplicatedRequests.Inc()
	}
}

// processResult handles the final result and updates provider health status
func (m *Manager) processResult(r *result) error {
	if r.name != "" {
		m.UpdateHealthStatus(r.name, r.status)
	}
	return r.err
}
