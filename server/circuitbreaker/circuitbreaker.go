// Package circuitbreaker provides an implementation of a circuit breaker pattern
// to manage service calls and handle failures gracefully.

package circuitbreaker

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
)

// Config represents the configuration settings for a CircuitBreaker instance.
type Config struct {
	// Name is the unique identifier for the circuit breaker.
	Name string
	// MaxRequests is the maximum number of requests allowed within the Interval.
	MaxRequests uint32
	// Interval is the time window for measuring the number of requests.
	Interval time.Duration
	// Timeout is the time limit for a single request.
	Timeout time.Duration
	// FailureThreshold is the number of consecutive failures required to trip the circuit breaker.
	FailureThreshold uint32
	// TestMode indicates whether the circuit breaker is running in test mode.
	TestMode bool
}

// CircuitBreaker represents a circuit breaker instance with its configuration and state.
type CircuitBreaker struct {
	// name is the unique identifier for the circuit breaker.
	name string
	// logger is the logger instance for logging events.
	logger *zap.Logger
	// metrics holds Prometheus metrics for the circuit breaker.
	metrics *metrics
	// breaker is the underlying gobreaker instance.
	breaker *gobreaker.CircuitBreaker
}

// metrics holds Prometheus metrics for the circuit breaker.
type metrics struct {
	// stateGauge tracks the current state of the circuit breaker.
	stateGauge prometheus.Gauge
	// failureCount tracks the total number of failures.
	failureCount prometheus.Counter
	// tripsTotal tracks the total number of times the circuit breaker has tripped.
	tripsTotal prometheus.Counter
}

// initCircuitBreaker initializes a new CircuitBreaker instance and sets up metrics.
// It returns the initialized CircuitBreaker and any error encountered during initialization.
func initCircuitBreaker(config Config, logger *zap.Logger, registry *prometheus.Registry) (*CircuitBreaker, error) {
	// Check if the circuit breaker name is empty.
	if config.Name == "" {
		return nil, fmt.Errorf("circuit breaker name cannot be empty")
	}

	// Create a new CircuitBreaker instance.
	cb := &CircuitBreaker{
		name:   config.Name,
		logger: logger,
	}

	// Initialize metrics if not in test mode.
	if registry != nil && !config.TestMode {
		// Create a new metrics instance.
		cb.metrics = &metrics{
			stateGauge: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "circuit_breaker_state",
				Help: "Current state of the circuit breaker (0=closed, 1=half-open, 2=open)",
				ConstLabels: prometheus.Labels{
					"name": config.Name,
				},
			}),
			failureCount: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "circuit_breaker_failures_total",
				Help: "Total number of failures",
				ConstLabels: prometheus.Labels{
					"name": config.Name,
				},
			}),
			tripsTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "circuit_breaker_trips_total",
				Help: "Total number of times the circuit breaker has tripped",
				ConstLabels: prometheus.Labels{
					"name": config.Name,
				},
			}),
		}

		// Register metrics with the Prometheus registry.
		registry.MustRegister(cb.metrics.stateGauge)
		registry.MustRegister(cb.metrics.failureCount)
		registry.MustRegister(cb.metrics.tripsTotal)
	}

	return cb, nil
}

// configureCircuitBreaker sets the configuration settings for the CircuitBreaker instance.
// It configures the gobreaker settings, including the trip conditions and state change handlers.
func configureCircuitBreaker(cb *CircuitBreaker, config Config, logger *zap.Logger) {
	// Create a new gobreaker settings instance.
	settings := gobreaker.Settings{
		Name:        config.Name,
		MaxRequests: config.MaxRequests,
		Interval:    config.Interval,
		Timeout:     config.Timeout,

		// ReadyToTrip determines if the circuit breaker should trip based on consecutive failures.
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// Check if the number of consecutive failures exceeds the threshold.
			shouldTrip := counts.ConsecutiveFailures >= config.FailureThreshold
			if shouldTrip {
				// Log a message when the circuit breaker trips.
				logger.Info("Circuit breaker tripping",
					zap.String("name", config.Name),
					zap.Uint32("consecutive_failures", counts.ConsecutiveFailures),
					zap.Uint32("threshold", config.FailureThreshold))
			}
			return shouldTrip
		},

		// OnStateChange handles actions to take when the circuit breaker state changes.
		OnStateChange: func(name string, from, to gobreaker.State) {
			// Log a message when the circuit breaker state changes.
			logger.Info("Circuit breaker state changed",
				zap.String("name", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()))

			// Update metrics based on the new state.
			if cb.metrics != nil {
				switch to {
				case gobreaker.StateOpen:
					cb.metrics.stateGauge.Set(2)
					cb.metrics.tripsTotal.Inc()
				case gobreaker.StateHalfOpen:
					cb.metrics.stateGauge.Set(1)
				case gobreaker.StateClosed:
					cb.metrics.stateGauge.Set(0)
				}
			}
		},
	}

	// Create a new gobreaker instance with the configured settings.
	cb.breaker = gobreaker.NewCircuitBreaker(settings)
}

// NewCircuitBreaker creates a new CircuitBreaker instance and configures it with the provided settings.
// It returns the configured CircuitBreaker instance and any error that occurred during initialization.
func NewCircuitBreaker(config Config, logger *zap.Logger, registry *prometheus.Registry) (*CircuitBreaker, error) {
	// Initialize the CircuitBreaker instance.
	cb, err := initCircuitBreaker(config, logger, registry)
	if err != nil {
		return nil, err
	}

	// Configure the CircuitBreaker instance with the provided settings.
	configureCircuitBreaker(cb, config, logger)

	return cb, nil
}

// Execute executes a function within the circuit breaker.
// It returns any error that occurred during execution.
func (cb *CircuitBreaker) Execute(operation func() error) error {
	// Execute the function within the circuit breaker.
	result, err := cb.breaker.Execute(func() (interface{}, error) {
		// Call the operation function.
		if err := operation(); err != nil {
			// Increment the failure count if the operation fails.
			if cb.metrics != nil {
				cb.metrics.failureCount.Inc()
			}
			// Log a message when the operation fails.
			cb.logger.Debug("Operation failed",
				zap.String("name", cb.name),
				zap.Error(err))
			return nil, err
		}
		return nil, nil
	})

	// Check if the circuit breaker is open.
	if err != nil {
		if err == gobreaker.ErrOpenState {
			// Log a message when the circuit breaker is open.
			cb.logger.Debug("Circuit breaker is open",
				zap.String("name", cb.name))
		}
		return err
	}

	// Ignore the result since we don't use it.
	_ = result
	return nil
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() gobreaker.State {
	return cb.breaker.State()
}

// Counts returns the current counts of the circuit breaker.
func (cb *CircuitBreaker) Counts() gobreaker.Counts {
	return cb.breaker.Counts()
}
