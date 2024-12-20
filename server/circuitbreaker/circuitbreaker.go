package circuitbreaker

import (
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

// Config holds configuration for the circuit breaker
type Config struct {
	FailureThreshold    int           // Number of failures before opening circuit
	ResetTimeout       time.Duration  // Time to wait before attempting reset
	HalfOpenRequests   int           // Number of requests to allow in half-open state
	TestMode          bool           // Skip metric registration in test mode
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name        string
	config      Config
	state       State
	failures    int
	lastFailure time.Time
	halfOpen    int
	mu          sync.RWMutex
	logger      *zap.Logger

	// Metrics
	stateGauge    prometheus.Gauge
	failuresCount prometheus.Counter
	tripsTotal    prometheus.Counter
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(name string, config Config, logger *zap.Logger, registry *prometheus.Registry) *CircuitBreaker {
	cb := &CircuitBreaker{
		name:   name,
		config: config,
		state:  StateClosed,
		logger: logger,
	}

	// Initialize Prometheus metrics
	cb.stateGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hapax_circuit_breaker_state",
		Help: "Current state of the circuit breaker (0=closed, 1=open, 2=half-open)",
		ConstLabels: prometheus.Labels{
			"name": name,
		},
	})

	cb.failuresCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hapax_circuit_breaker_failures_total",
		Help: "Total number of failures recorded by the circuit breaker",
		ConstLabels: prometheus.Labels{
			"name": name,
		},
	})

	cb.tripsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hapax_circuit_breaker_trips_total",
		Help: "Total number of times the circuit breaker has tripped",
		ConstLabels: prometheus.Labels{
			"name": name,
		},
	})

	// Register metrics with Prometheus if not in test mode
	if !config.TestMode && registry != nil {
		registry.MustRegister(cb.stateGauge)
		registry.MustRegister(cb.failuresCount)
		registry.MustRegister(cb.tripsTotal)
	}

	return cb
}

// Execute runs the given function if the circuit breaker allows it
func (cb *CircuitBreaker) Execute(f func() error) error {
	if !cb.AllowRequest() {
		return ErrCircuitOpen
	}

	err := f()
	cb.RecordResult(err)
	return err
}

// AllowRequest checks if a request should be allowed through
func (cb *CircuitBreaker) AllowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		// Check if enough time has passed to try half-open
		if time.Since(cb.lastFailure) > cb.config.ResetTimeout {
			cb.setState(StateHalfOpen)
			cb.halfOpen = 0
			return true
		}
		return false
	case StateHalfOpen:
		// Allow one request in half-open state
		if cb.halfOpen < cb.config.HalfOpenRequests {
			cb.halfOpen++
			return true
		}
		return false
	default:
		return false
	}
}

// RecordResult records the result of a request
func (cb *CircuitBreaker) RecordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.failuresCount.Inc()
		cb.lastFailure = time.Now()

		// Trip breaker if failure threshold reached
		if cb.failures >= cb.config.FailureThreshold {
			cb.tripBreaker()
		}
	} else {
		// Reset on success
		if cb.state == StateHalfOpen {
			cb.setState(StateClosed)
			cb.failures = 0
			cb.halfOpen = 0
		} else if cb.state == StateClosed {
			cb.failures = 0
		}
	}
}

// tripBreaker moves the circuit breaker to the open state
func (cb *CircuitBreaker) tripBreaker() {
	cb.setState(StateOpen)
	cb.tripsTotal.Inc()
	cb.logger.Warn("Circuit breaker tripped",
		zap.String("name", cb.name),
		zap.Int("failures", cb.failures),
		zap.Time("last_failure", cb.lastFailure),
	)
}

// setState updates the circuit breaker state and metrics
func (cb *CircuitBreaker) setState(state State) {
	cb.state = state
	cb.stateGauge.Set(float64(state))
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}
