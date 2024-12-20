// Package provider implements LLM provider management functionality.
package provider

import (
	"context"
	"sync"
	"time"

	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"go.uber.org/zap"
)

// HealthStatus represents the current health state of a provider
type HealthStatus struct {
	Healthy          bool
	LastCheck        time.Time
	ConsecutiveFails int
	Latency         time.Duration
	ErrorCount      int64
	RequestCount    int64
}

// Manager handles LLM provider management, including:
// - Health monitoring
// - Failover handling
// - Provider configuration
type Manager struct {
	providers    map[string]gollm.LLM
	healthStates sync.Map
	logger       *zap.Logger
	cfg          *config.Config
	mu           sync.RWMutex
}

// NewManager creates a new provider manager instance
func NewManager(cfg *config.Config, logger *zap.Logger) (*Manager, error) {
	m := &Manager{
		providers: make(map[string]gollm.LLM),
		logger:    logger,
		cfg:       cfg,
	}

	// Skip provider initialization if health check is disabled
	if cfg.LLM.HealthCheck == nil || !cfg.LLM.HealthCheck.Enabled {
		return m, nil
	}

	// Initialize providers from config
	if err := m.initializeProviders(); err != nil {
		return nil, err
	}

	return m, nil
}

// NewManagerWithProviders creates a manager with pre-configured providers (for testing)
func NewManagerWithProviders(cfg *config.Config, logger *zap.Logger, providers map[string]gollm.LLM) *Manager {
	return &Manager{
		providers: providers,
		logger:    logger,
		cfg:       cfg,
	}
}

// initializeProviders sets up LLM providers based on configuration
func (m *Manager) initializeProviders() error {
	// Initialize primary provider
	primary, err := gollm.NewLLM(
		gollm.SetProvider(m.cfg.LLM.Provider),
		gollm.SetModel(m.cfg.LLM.Model),
	)
	if err != nil {
		return err
	}
	m.providers[m.cfg.LLM.Provider] = primary

	// Initialize backup providers if configured
	for _, backup := range m.cfg.LLM.BackupProviders {
		llm, err := gollm.NewLLM(
			gollm.SetProvider(backup.Provider),
			gollm.SetModel(backup.Model),
		)
		if err != nil {
			m.logger.Warn("Failed to initialize backup provider", 
				zap.String("provider", backup.Provider),
				zap.Error(err))
			continue
		}
		m.providers[backup.Provider] = llm
	}

	return nil
}

// StartHealthChecks begins monitoring all providers
func (m *Manager) StartHealthChecks(ctx context.Context) {
	for providerName, llm := range m.providers {
		go m.monitorProvider(ctx, providerName, llm)
	}
}

// monitorProvider continuously monitors a single provider's health
func (m *Manager) monitorProvider(ctx context.Context, providerName string, llm gollm.LLM) {
	ticker := time.NewTicker(m.cfg.LLM.HealthCheck.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status := m.checkProviderHealth(providerName, llm)
			m.updateHealthStatus(providerName, status)
		}
	}
}

// checkProviderHealth performs a health check on a provider
func (m *Manager) checkProviderHealth(providerName string, llm gollm.LLM) HealthStatus {
	start := time.Now()
	
	// Simple health check prompt
	prompt := llm.NewPrompt("health check")
	llm.SetSystemPrompt("Respond with 'ok' for health check.", gollm.CacheTypeEphemeral)

	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.LLM.HealthCheck.Timeout)
	defer cancel()

	_, err := llm.Generate(ctx, prompt)
	
	status := HealthStatus{
		LastCheck: time.Now(),
		Latency:   time.Since(start),
	}

	if err != nil {
		m.logger.Warn("Provider health check failed",
			zap.String("provider", providerName),
			zap.Error(err))
		status.Healthy = false
		status.ConsecutiveFails++
	} else {
		status.Healthy = true
		status.ConsecutiveFails = 0
	}

	return status
}

// updateHealthStatus updates the health status for a provider
func (m *Manager) updateHealthStatus(providerName string, status HealthStatus) {
	m.healthStates.Store(providerName, status)
}

// GetHealthyProvider returns a healthy provider, implementing failover if needed
func (m *Manager) GetHealthyProvider() (gollm.LLM, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Try primary provider first
	if status, ok := m.getProviderStatus(m.cfg.LLM.Provider); ok && status.Healthy {
		return m.providers[m.cfg.LLM.Provider], nil
	}

	// Try backup providers in order
	for _, backup := range m.cfg.LLM.BackupProviders {
		if status, ok := m.getProviderStatus(backup.Provider); ok && status.Healthy {
			return m.providers[backup.Provider], nil
		}
	}

	return nil, ErrNoHealthyProvider
}

// getProviderStatus retrieves the current health status for a provider
func (m *Manager) getProviderStatus(provider string) (HealthStatus, bool) {
	if status, ok := m.healthStates.Load(provider); ok {
		return status.(HealthStatus), true
	}
	return HealthStatus{}, false
}
