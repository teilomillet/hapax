package provider

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/teilomillet/gollm"
	"go.uber.org/zap"
)

// HealthStatus represents the current health state of a provider
// Fields: Healthy, LastCheck, ConsecutiveFails, Latency, ErrorCount, RequestCount

// HealthStatus represents the current health state of a provider
type HealthStatus struct {
	Healthy          bool          // Whether the provider is currently healthy
	LastCheck        time.Time     // When the last health check was performed
	ConsecutiveFails int           // Number of consecutive failures
	Latency          time.Duration // Last observed latency
	ErrorCount       int64         // Total number of errors
	RequestCount     int64         // Total number of requests
}

// startHealthChecks begins monitoring all providers
func (m *Manager) startHealthChecks(ctx context.Context) {
	interval := time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkAllProviders()
		}
	}
}

// checkAllProviders performs health checks on all providers
func (m *Manager) checkAllProviders() {
	for name, provider := range m.providers {
		start := time.Now()

		// Get the current health status
		var status HealthStatus
		if val, ok := m.healthStates.Load(name); ok {
			status = val.(HealthStatus)
		}

		// Perform health check
		err := m.healthCheck(provider)
		duration := time.Since(start)

		// Update metrics
		m.healthCheckDuration.Observe(duration.Seconds())

		if err != nil {
			m.healthCheckErrors.WithLabelValues(name).Inc()
			status.Healthy = false
			status.ErrorCount++
		} else {
			status.Healthy = true
			status.ErrorCount = 0
		}

		status.LastCheck = time.Now()
		m.UpdateHealthStatus(name, status)
	}
}

// CheckProviderHealth performs a health check on a provider
func (m *Manager) CheckProviderHealth(name string, llm gollm.LLM) HealthStatus {
	return m.checkProviderHealth(name, llm)
}

// checkProviderHealth performs a health check on a provider
func (m *Manager) checkProviderHealth(name string, llm gollm.LLM) HealthStatus {
	start := time.Now()
	status := HealthStatus{
		LastCheck: start,
		Healthy:   true,
	}

	// Get previous status if any
	if val, ok := m.healthStates.Load(name); ok {
		prevStatus := val.(HealthStatus)
		status.ConsecutiveFails = prevStatus.ConsecutiveFails
		status.ErrorCount = prevStatus.ErrorCount
		status.RequestCount = prevStatus.RequestCount
	}

	// Simple health check prompt
	prompt := &gollm.Prompt{
		Messages: []gollm.PromptMessage{
			{Role: "user", Content: "health check"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := llm.Generate(ctx, prompt)
	status.Latency = time.Since(start)
	m.healthCheckDuration.Observe(status.Latency.Seconds())

	if err != nil {
		status.Healthy = false
		status.ConsecutiveFails++
		status.ErrorCount++
		m.healthCheckErrors.WithLabelValues(name).Inc()
		m.logger.Warn("Provider health check failed",
			zap.String("provider", name),
			zap.Error(err),
			zap.Duration("latency", status.Latency),
		)
	} else {
		status.ConsecutiveFails = 0
	}

	status.RequestCount++
	return status
}

// GetHealthCheckErrors returns the health check errors counter for testing
func (m *Manager) GetHealthCheckErrors() *prometheus.CounterVec {
	return m.healthCheckErrors
}

// GetHealthStatus returns the health status for a provider
func (m *Manager) GetHealthStatus(name string) HealthStatus {
	if val, ok := m.healthStates.Load(name); ok {
		return val.(HealthStatus)
	}
	return HealthStatus{}
}

// UpdateHealthStatus updates the health status for a provider
func (m *Manager) UpdateHealthStatus(name string, status HealthStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get the current status
	var currentStatus HealthStatus
	if val, ok := m.healthStates.Load(name); ok {
		currentStatus = val.(HealthStatus)
	}

	// Update the status
	newStatus := HealthStatus{
		Healthy:    status.Healthy,
		LastCheck:  status.LastCheck,
		ErrorCount: status.ErrorCount,
	}

	// If the status is becoming healthy, reset error count
	if status.Healthy && !currentStatus.Healthy {
		newStatus.ErrorCount = 0
	}

	// Store the new status
	m.healthStates.Store(name, newStatus)

	// Update metrics
	if status.Healthy {
		m.healthyProviders.WithLabelValues(name).Set(1)
	} else {
		m.healthyProviders.WithLabelValues(name).Set(0)
	}
}

func (m *Manager) healthCheck(provider gollm.LLM) error {
	// Simple health check prompt
	prompt := &gollm.Prompt{
		Messages: []gollm.PromptMessage{
			{Role: "user", Content: "health check"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := provider.Generate(ctx, prompt)
	return err
}

// PerformHealthCheck performs a health check on all providers
func (m *Manager) PerformHealthCheck() {
	for name, provider := range m.providers {
		start := time.Now()

		// Get the current health status
		var status HealthStatus
		if val, ok := m.healthStates.Load(name); ok {
			status = val.(HealthStatus)
		}

		// Perform health check
		err := m.healthCheck(provider)
		duration := time.Since(start)

		// Update metrics
		m.healthCheckDuration.Observe(duration.Seconds())

		if err != nil {
			m.healthCheckErrors.WithLabelValues(name).Inc()
			status.Healthy = false
			status.ErrorCount++
		} else {
			status.Healthy = true
			status.ErrorCount = 0
		}

		status.LastCheck = time.Now()
		m.UpdateHealthStatus(name, status)
	}
}
