package circuitbreaker

import (
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// State represents the current state of the circuit breaker
type State int

const (
	StateClosed State = iota    // Circuit is closed (allowing requests)
	StateOpen                   // Circuit is open (blocking requests)
	StateHalfOpen              // Circuit is half-open (testing if service is healthy)
)

// String returns a string representation of the circuit breaker state
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Config holds configuration for the circuit breaker
type Config struct {
	FailureThreshold    int           // Number of failures before tripping
	ResetTimeout       time.Duration  // Time to wait before attempting reset
	HalfOpenRequests   int           // Number of requests to allow in half-open state (default 1)
	TestMode          bool           // Skip metric registration in test mode
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	mu              sync.RWMutex
	name            string
	state           State
	lastFailure     time.Time
	failures        int
	halfOpenFailures int  // Track failures in half-open state separately
	halfOpenAllowed bool
	config          Config
	logger          *zap.Logger

	// Prometheus metrics
	stateGauge     prometheus.Gauge
	failuresCount  prometheus.Counter
	tripsTotal     prometheus.Counter

	// Callback for state changes
	onStateChange func(State)
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration
func NewCircuitBreaker(name string, config Config, logger *zap.Logger, registry *prometheus.Registry) *CircuitBreaker {
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 5
	}
	if config.ResetTimeout == 0 {
		config.ResetTimeout = 30 * time.Second
	}
	if config.HalfOpenRequests == 0 {
		config.HalfOpenRequests = 1
	}

	cb := &CircuitBreaker{
		name:            name,
		state:           StateClosed,
		config:          config,
		logger:          logger,
		halfOpenAllowed: false,
	}

	// Initialize metrics
	if registry != nil && !config.TestMode {
		cb.stateGauge = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "circuit_breaker_state",
			Help: "Current state of the circuit breaker (0=closed, 1=half-open, 2=open)",
			ConstLabels: prometheus.Labels{
				"name": name,
			},
		})
		cb.failuresCount = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "circuit_breaker_failures_total",
			Help: "Total number of failures",
			ConstLabels: prometheus.Labels{
				"name": name,
			},
		})
		cb.tripsTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "circuit_breaker_trips_total",
			Help: "Total number of times the circuit breaker has tripped",
			ConstLabels: prometheus.Labels{
				"name": name,
			},
		})

		registry.MustRegister(cb.stateGauge)
		registry.MustRegister(cb.failuresCount)
		registry.MustRegister(cb.tripsTotal)
	}

	return cb
}

// Execute runs the given function if the circuit breaker allows it
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if !cb.AllowRequest() {
		cb.logger.Debug("request rejected by circuit breaker",
			zap.String("name", cb.name))
		// Record this as a failure in half-open state
		if cb.GetState() == StateHalfOpen {
			cb.RecordResult(errors.New("circuit breaker is open"))
		}
		return errors.New("circuit breaker is open")
	}

	err := fn()
	cb.RecordResult(err)
	return err
}

// AllowRequest returns true if the request should be allowed
func (cb *CircuitBreaker) AllowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Always allow in closed state
	if cb.state == StateClosed {
		cb.logger.Debug("circuit breaker is closed, allowing request",
			zap.String("name", cb.name))
		return true
	}

	// In open state, check if we should transition to half-open
	if cb.state == StateOpen {
		if time.Since(cb.lastFailure) > cb.config.ResetTimeout {
			oldState := cb.state
			cb.state = StateHalfOpen
			cb.halfOpenAllowed = true  // Allow the first request
			// Do not reset failures or halfOpenFailures when entering half-open state

			if cb.stateGauge != nil {
				cb.stateGauge.Set(float64(StateHalfOpen))
			}

			// Do callbacks and logging
			if cb.onStateChange != nil {
				cb.onStateChange(StateHalfOpen)
			}
			cb.logger.Debug("circuit breaker transitioning to half-open",
				zap.String("name", cb.name))
			cb.logger.Debug("circuit breaker state changed",
				zap.String("name", cb.name),
				zap.String("old_state", oldState.String()),
				zap.String("new_state", StateHalfOpen.String()))
		} else {
			cb.logger.Debug("circuit breaker is open, rejecting request",
				zap.String("name", cb.name),
				zap.Time("last_failure", cb.lastFailure),
				zap.Duration("reset_timeout", cb.config.ResetTimeout))
			return false
		}
	}

	// In half-open state, only allow one request
	if cb.state == StateHalfOpen {
		if cb.halfOpenAllowed {
			cb.halfOpenAllowed = false  // Don't allow more requests until success
			cb.logger.Debug("allowing request in half-open state",
				zap.String("name", cb.name))
			return true
		}
		cb.logger.Debug("rejecting request in half-open state",
			zap.String("name", cb.name))
		return false
	}

	return false
}

// RecordResult records the result of a request
func (cb *CircuitBreaker) RecordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		if cb.state == StateClosed {
			cb.failures++
			cb.logger.Debug("recorded failure",
				zap.String("name", cb.name),
				zap.Int("failures", cb.failures),
				zap.Int("threshold", cb.config.FailureThreshold))

			if cb.failures >= cb.config.FailureThreshold {
				oldState := cb.state
				cb.state = StateOpen
				cb.lastFailure = time.Now()
				// Don't reset half-open failures when going to open from closed

				if cb.stateGauge != nil {
					cb.stateGauge.Set(float64(StateOpen))
				}

				// Do callbacks and logging
				if cb.onStateChange != nil {
					cb.onStateChange(StateOpen)
				}
				cb.logger.Warn("Circuit breaker tripped",
					zap.String("name", cb.name),
					zap.Int("failures", cb.failures),
					zap.Time("last_failure", cb.lastFailure))
				cb.logger.Debug("circuit breaker state changed",
					zap.String("name", cb.name),
					zap.String("old_state", oldState.String()),
					zap.String("new_state", StateOpen.String()))
			}
		} else if cb.state == StateHalfOpen {
			cb.halfOpenFailures = 1  // Set to 1 to indicate our test request failed
			oldState := cb.state
			cb.state = StateOpen
			cb.lastFailure = time.Now()
			cb.halfOpenAllowed = false  // Don't allow more requests until next timeout

			if cb.stateGauge != nil {
				cb.stateGauge.Set(float64(StateOpen))
			}

			// Do callbacks and logging
			if cb.onStateChange != nil {
				cb.onStateChange(StateOpen)
			}
			cb.logger.Debug("failed request in half-open state, returning to open",
				zap.String("name", cb.name))
			cb.logger.Debug("circuit breaker state changed",
				zap.String("name", cb.name),
				zap.String("old_state", oldState.String()),
				zap.String("new_state", StateOpen.String()))
		}
	} else {
		if cb.state == StateHalfOpen {
			oldState := cb.state
			cb.state = StateClosed
			cb.failures = 0
			cb.halfOpenFailures = 0  // Reset half-open failures on success
			cb.halfOpenAllowed = false

			if cb.stateGauge != nil {
				cb.stateGauge.Set(float64(StateClosed))
			}

			// Do callbacks and logging
			if cb.onStateChange != nil {
				cb.onStateChange(StateClosed)
			}
			cb.logger.Debug("successful request in half-open state, transitioning to closed",
				zap.String("name", cb.name))
			cb.logger.Debug("circuit breaker state changed",
				zap.String("name", cb.name),
				zap.String("old_state", oldState.String()),
				zap.String("new_state", StateClosed.String()))
		}
	}
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetHalfOpenFailures returns the number of failures in half-open state
func (cb *CircuitBreaker) GetHalfOpenFailures() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.halfOpenFailures
}

// SetStateChangeCallback sets a callback to be called when the circuit breaker state changes
func (cb *CircuitBreaker) SetStateChangeCallback(callback func(State)) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.onStateChange = callback
}
