package provider

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sony/gobreaker"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/circuitbreaker"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

// HealthStatus represents the current health state of a provider
type HealthStatus struct {
	Healthy          bool          // Whether the provider is currently healthy
	LastCheck        time.Time     // When the last health check was performed
	ConsecutiveFails int           // Number of consecutive failures
	Latency          time.Duration // Last observed latency
	ErrorCount       int64         // Total number of errors
	RequestCount     int64         // Total number of requests
}

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
	opCounter            atomic.Int64       // Counter for generating unique operation keys
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

	m.deduplicatedRequests = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hapax_deduplicated_requests_total",
		Help: "Number of deduplicated requests",
	})

	m.healthyProviders = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hapax_healthy_providers",
		Help: "Number of healthy providers",
	}, []string{"provider"})

	registry.MustRegister(m.healthCheckDuration)
	registry.MustRegister(m.healthCheckErrors)
	registry.MustRegister(m.requestLatency)
	registry.MustRegister(m.deduplicatedRequests)
	registry.MustRegister(m.healthyProviders)
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

// maxProviderRetries defines the maximum number of times we'll retry through the provider list
// before giving up. This prevents infinite loops when all providers are unhealthy.
const maxProviderRetries = 3

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

func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
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
