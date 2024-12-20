package provider

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/circuitbreaker"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/teilomillet/gollm"
	"go.uber.org/zap"
)

// HealthStatus represents the current health state of a provider
type HealthStatus struct {
	Healthy          bool      // Whether the provider is currently healthy
	LastCheck        time.Time // When the last health check was performed
	ConsecutiveFails int       // Number of consecutive failures
	Latency         time.Duration // Last observed latency
	ErrorCount      int64      // Total number of errors
	RequestCount    int64      // Total number of requests
}

// Manager handles LLM provider management and selection
type Manager struct {
	providers    map[string]gollm.LLM
	breakers     map[string]*circuitbreaker.CircuitBreaker
	healthStates sync.Map // map[string]HealthStatus
	logger       *zap.Logger
	cfg          *config.Config
	mu           sync.RWMutex

	// Metrics
	registry           *prometheus.Registry
	healthCheckDuration prometheus.Histogram
	healthCheckErrors   *prometheus.CounterVec
	requestLatency     *prometheus.HistogramVec
}

// NewManager creates a new provider manager
func NewManager(cfg *config.Config, logger *zap.Logger, registry *prometheus.Registry) (*Manager, error) {
	m := &Manager{
		providers: make(map[string]gollm.LLM),
		breakers:  make(map[string]*circuitbreaker.CircuitBreaker),
		logger:    logger,
		cfg:       cfg,
		registry:  registry,
	}

	// Initialize metrics
	m.initializeMetrics(registry)

	// Create circuit breaker config
	cbConfig := circuitbreaker.Config{
		FailureThreshold:  3,           // Trip after 3 failures
		ResetTimeout:     cfg.CircuitBreaker.ResetTimeout,  // Use configured timeout
		HalfOpenRequests: 1,           // Allow 1 request in half-open state
	}

	if cbConfig.ResetTimeout == 0 {
		cbConfig.ResetTimeout = time.Minute // Default to 1 minute
	}

	// Initialize providers from both new and legacy configs
	if !cfg.TestMode {
		if err := m.initializeProviders(cbConfig, registry); err != nil {
			return nil, err
		}
	}

	// Start health checks if enabled
	if cfg.LLM.HealthCheck != nil && cfg.LLM.HealthCheck.Enabled {
		go m.startHealthChecks(context.Background())
	}

	return m, nil
}

// initializeMetrics sets up Prometheus metrics
func (m *Manager) initializeMetrics(registry *prometheus.Registry) {
	m.healthCheckDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "hapax_health_check_duration_seconds",
		Help: "Duration of provider health checks",
	})

	m.healthCheckErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hapax_health_check_errors_total",
		Help: "Number of health check errors by provider",
	}, []string{"provider"})

	m.requestLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hapax_request_latency_seconds",
		Help: "Latency of provider requests",
	}, []string{"provider"})

	registry.MustRegister(m.healthCheckDuration)
	registry.MustRegister(m.healthCheckErrors)
	registry.MustRegister(m.requestLatency)
}

// initializeProviders sets up LLM providers based on configuration
func (m *Manager) initializeProviders(cbConfig circuitbreaker.Config, registry *prometheus.Registry) error {
	// Initialize from new provider config
	for name, providerCfg := range m.cfg.Providers {
		llm, err := gollm.NewLLM(
			gollm.SetProvider(providerCfg.Type),
			gollm.SetModel(providerCfg.Model),
			gollm.SetAPIKey(providerCfg.APIKey),
		)
		if err != nil {
			return fmt.Errorf("failed to initialize provider %s: %w", name, err)
		}

		m.providers[name] = llm
		m.breakers[name] = circuitbreaker.NewCircuitBreaker(
			name,
			cbConfig,
			m.logger.With(zap.String("provider", name)),
			registry,
		)
	}

	// Initialize from legacy config if no new providers configured
	if len(m.providers) == 0 && m.cfg.LLM.Provider != "" {
		primary, err := gollm.NewLLM(
			gollm.SetProvider(m.cfg.LLM.Provider),
			gollm.SetModel(m.cfg.LLM.Model),
		)
		if err != nil {
			return fmt.Errorf("failed to initialize legacy provider: %w", err)
		}

		name := m.cfg.LLM.Provider
		m.providers[name] = primary
		m.breakers[name] = circuitbreaker.NewCircuitBreaker(
			name,
			cbConfig,
			m.logger.With(zap.String("provider", name)),
			registry,
		)

		// Initialize legacy backup providers
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
			m.breakers[backup.Provider] = circuitbreaker.NewCircuitBreaker(
				backup.Provider,
				cbConfig,
				m.logger.With(zap.String("provider", backup.Provider)),
				registry,
			)
		}
	}

	return nil
}

// startHealthChecks begins monitoring all providers
func (m *Manager) startHealthChecks(ctx context.Context) {
	interval := time.Minute
	if m.cfg.LLM.HealthCheck != nil {
		interval = m.cfg.LLM.HealthCheck.Interval
	}

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
		status := m.checkProviderHealth(name, provider)
		m.updateHealthStatus(name, status)
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

// UpdateHealthStatus updates the health status for a provider
func (m *Manager) UpdateHealthStatus(name string, status HealthStatus) {
	m.updateHealthStatus(name, status)
}

// updateHealthStatus updates the health status for a provider
func (m *Manager) updateHealthStatus(name string, status HealthStatus) {
	m.healthStates.Store(name, status)
}

// GetProvider returns a healthy provider or error if none available
func (m *Manager) GetProvider() (gollm.LLM, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Try providers in order of preference
	for _, name := range m.cfg.ProviderPreference {
		provider, ok := m.providers[name]
		if !ok {
			continue
		}

		// Check circuit breaker state
		breaker := m.breakers[name]
		if !breaker.AllowRequest() {
			continue
		}

		// Check health status
		if val, ok := m.healthStates.Load(name); ok {
			status := val.(HealthStatus)
			if !status.Healthy {
				continue
			}
		}

		return provider, nil
	}

	return nil, ErrNoHealthyProvider
}

// Execute runs an LLM operation with circuit breaker protection
func (m *Manager) Execute(ctx context.Context, op func(gollm.LLM) error) error {
	provider, err := m.GetProvider()
	if err != nil {
		return fmt.Errorf("failed to get provider: %w", err)
	}

	name := m.getProviderName(provider)
	breaker := m.breakers[name]

	start := time.Now()
	err = breaker.Execute(func() error {
		return op(provider)
	})
	m.requestLatency.WithLabelValues(name).Observe(time.Since(start).Seconds())

	// If all providers are failing, wrap with ErrNoHealthyProvider
	if err != nil {
		allFailing := true
		for _, b := range m.breakers {
			if b.GetState() != circuitbreaker.StateOpen {
				allFailing = false
				break
			}
		}
		if allFailing {
			return fmt.Errorf("%w: %v", ErrNoHealthyProvider, err)
		}
	}

	return err
}

// getProviderName returns the name of a provider instance
func (m *Manager) getProviderName(provider gollm.LLM) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, p := range m.providers {
		if p == provider {
			return name
		}
	}
	return "unknown"
}

// SetProviders replaces the current providers with new ones (for testing)
func (m *Manager) SetProviders(providers map[string]gollm.LLM) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.providers = providers

	// Create circuit breaker config
	cbConfig := circuitbreaker.Config{
		FailureThreshold:  3,           // Trip after 3 failures
		ResetTimeout:     m.cfg.CircuitBreaker.ResetTimeout,  // Use configured timeout
		HalfOpenRequests: 1,           // Allow 1 request in half-open state
		TestMode:        false,        // Enable metrics in SetProviders
	}

	if cbConfig.ResetTimeout == 0 {
		cbConfig.ResetTimeout = time.Minute // Default to 1 minute
	}

	// Initialize circuit breakers
	for name := range providers {
		m.breakers[name] = circuitbreaker.NewCircuitBreaker(
			name,
			cbConfig,
			m.logger.With(zap.String("provider", name)),
			m.registry, // Use the same registry as the manager
		)
	}
}

// GetHealthCheckErrors returns the health check errors counter for testing
func (m *Manager) GetHealthCheckErrors() *prometheus.CounterVec {
	return m.healthCheckErrors
}
