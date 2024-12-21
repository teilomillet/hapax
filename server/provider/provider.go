package provider

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sony/gobreaker"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/circuitbreaker"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

// maxProviderRetries defines the maximum number of times we'll retry through the provider list
// before giving up. This prevents infinite loops when all providers are unhealthy.
const maxProviderRetries = 3

// Manager handles LLM provider management and selection
type Manager struct {
	providers    map[string]gollm.LLM
	breakers     map[string]*circuitbreaker.CircuitBreaker
	healthStates sync.Map // map[string]HealthStatus
	logger       *zap.Logger
	cfg          *config.Config
	mu           sync.RWMutex
	group        *singleflight.Group // For deduplicating identical requests

	// Metrics
	registry             *prometheus.Registry
	healthCheckDuration  prometheus.Histogram
	healthCheckErrors    *prometheus.CounterVec
	requestLatency       *prometheus.HistogramVec
	deduplicatedRequests prometheus.Counter // New metric for tracking deduplicated requests
	healthyProviders     *prometheus.GaugeVec
}

// NewManager creates a new provider manager
func NewManager(cfg *config.Config, logger *zap.Logger, registry *prometheus.Registry) (*Manager, error) {
	m := &Manager{
		providers: make(map[string]gollm.LLM),
		breakers:  make(map[string]*circuitbreaker.CircuitBreaker),
		logger:    logger,
		cfg:       cfg,
		registry:  registry,
		group:     &singleflight.Group{},
	}

	// Initialize metrics
	m.initializeMetrics(registry)

	// Initialize providers from both new and legacy configs
	if !cfg.TestMode {
		if err := m.initializeProviders(); err != nil {
			return nil, err
		}
	}

	// Start health checks if enabled
	if cfg.LLM.HealthCheck != nil && cfg.LLM.HealthCheck.Enabled {
		go m.startHealthChecks(context.Background())
	}

	return m, nil
}

// initializeProviders sets up LLM providers based on configuration
func (m *Manager) initializeProviders() error {
	m.providers = make(map[string]gollm.LLM)
	m.breakers = make(map[string]*circuitbreaker.CircuitBreaker)

	for name, cfg := range m.cfg.Providers {
		provider, err := m.initializeProvider(name, cfg)
		if err != nil {
			return fmt.Errorf("failed to initialize provider %s: %w", name, err)
		}

		m.providers[name] = provider
		m.logger.Info("Created LLM",
			zap.String("provider", name),
			zap.String("model", cfg.Model),
			zap.Int("api_key_length", len(cfg.APIKey)))

		// Initialize provider as healthy
		m.UpdateHealthStatus(name, HealthStatus{
			Healthy:    true,
			LastCheck:  time.Now(),
			ErrorCount: 0,
		})

		// Initialize circuit breaker with gobreaker configuration
		cbConfig := circuitbreaker.Config{
			Name:             name,
			MaxRequests:      1,               // Allow 1 request in half-open state
			Interval:         time.Minute * 2, // Cyclic period of closed state
			Timeout:          time.Minute,     // Period of open state
			FailureThreshold: 3,               // Trip after 3 failures
			TestMode:         m.cfg.CircuitBreaker.TestMode,
		}

		// Override with config values if provided
		if m.cfg.CircuitBreaker.Timeout > 0 {
			cbConfig.Timeout = m.cfg.CircuitBreaker.Timeout
		}
		if m.cfg.CircuitBreaker.MaxRequests > 0 {
			cbConfig.MaxRequests = m.cfg.CircuitBreaker.MaxRequests
		}

		breaker, err := circuitbreaker.NewCircuitBreaker(cbConfig, m.logger, m.registry)
		if err != nil {
			return fmt.Errorf("failed to create circuit breaker for %s: %w", name, err)
		}
		m.breakers[name] = breaker
	}

	return nil
}

// initializeProvider initializes a single LLM provider
func (m *Manager) initializeProvider(name string, cfg config.ProviderConfig) (gollm.LLM, error) {
	provider, err := gollm.NewLLM(
		gollm.SetProvider(cfg.Type),
		gollm.SetModel(cfg.Model),
		gollm.SetAPIKey(cfg.APIKey),
	)
	if err != nil {
		return nil, err
	}

	return provider, nil
}

// GetProvider returns a healthy provider or error if none available
func (m *Manager) GetProvider() (gollm.LLM, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Try each provider in order of preference
	for _, name := range m.cfg.ProviderPreference {
		provider, exists := m.providers[name]
		if !exists {
			continue
		}

		// Skip if provider is unhealthy
		status := m.GetHealthStatus(name)
		if !status.Healthy {
			continue
		}

		// Skip if circuit breaker is open
		breaker := m.breakers[name]
		if breaker != nil && breaker.State() == gobreaker.StateOpen {
			continue
		}

		return provider, nil
	}

	return nil, fmt.Errorf("no healthy provider available")
}

// SetProviders replaces the current providers with new ones (for testing)
func (m *Manager) SetProviders(providers map[string]gollm.LLM) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear existing providers and breakers
	m.providers = make(map[string]gollm.LLM)
	m.breakers = make(map[string]*circuitbreaker.CircuitBreaker)

	// Set up new providers
	for name, provider := range providers {
		m.providers[name] = provider

		// Create circuit breaker for provider
		cbConfig := circuitbreaker.Config{
			Name:             name,
			MaxRequests:      1,
			Interval:         time.Second,
			Timeout:          m.cfg.CircuitBreaker.Timeout,
			FailureThreshold: 2,
			TestMode:         m.cfg.CircuitBreaker.TestMode,
		}

		breaker, err := circuitbreaker.NewCircuitBreaker(cbConfig, m.logger, m.registry)
		if err != nil {
			m.logger.Error("Failed to create circuit breaker",
				zap.String("provider", name),
				zap.Error(err))
			continue
		}

		m.breakers[name] = breaker

		// Initialize health status directly without calling UpdateHealthStatus
		m.healthStates.Store(name, HealthStatus{
			Healthy:    true,
			LastCheck:  time.Now(),
			ErrorCount: 0,
		})
	}

	// Create a map to track which providers have been added to the preference list
	added := make(map[string]bool)

	// Keep existing provider preference order for providers that still exist
	newPreference := make([]string, 0, len(providers))
	for _, name := range m.cfg.ProviderPreference {
		if _, exists := providers[name]; exists {
			newPreference = append(newPreference, name)
			added[name] = true
		}
	}

	// Add any new providers that weren't in the original preference list
	for name := range providers {
		if !added[name] {
			newPreference = append(newPreference, name)
		}
	}

	m.cfg.ProviderPreference = newPreference
	m.logger.Debug("updated provider preference list", zap.Strings("preference", newPreference))
}
