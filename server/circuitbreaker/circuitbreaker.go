package circuitbreaker

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
)

type Config struct {
	Name             string
	MaxRequests      uint32
	Interval         time.Duration
	Timeout          time.Duration
	FailureThreshold uint32
	TestMode         bool
}

type CircuitBreaker struct {
	name    string
	breaker *gobreaker.CircuitBreaker
	logger  *zap.Logger
	metrics *metrics
}

type metrics struct {
	stateGauge   prometheus.Gauge
	failureCount prometheus.Counter
	tripsTotal   prometheus.Counter
}

func NewCircuitBreaker(config Config, logger *zap.Logger, registry *prometheus.Registry) (*CircuitBreaker, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("circuit breaker name cannot be empty")
	}

	cb := &CircuitBreaker{
		name:   config.Name,
		logger: logger,
	}

	// Initialize metrics if not in test mode
	if registry != nil && !config.TestMode {
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

		registry.MustRegister(cb.metrics.stateGauge)
		registry.MustRegister(cb.metrics.failureCount)
		registry.MustRegister(cb.metrics.tripsTotal)
	}

	settings := gobreaker.Settings{
		Name:        config.Name,
		MaxRequests: config.MaxRequests,
		Interval:    config.Interval,
		Timeout:     config.Timeout,

		// Simplified ReadyToTrip that only looks at consecutive failures
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			shouldTrip := counts.ConsecutiveFailures >= config.FailureThreshold
			if shouldTrip {
				logger.Info("Circuit breaker tripping",
					zap.String("name", config.Name),
					zap.Uint32("consecutive_failures", counts.ConsecutiveFailures),
					zap.Uint32("threshold", config.FailureThreshold))
			}
			return shouldTrip
		},

		OnStateChange: func(name string, from, to gobreaker.State) {
			logger.Info("Circuit breaker state changed",
				zap.String("name", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()))

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

	cb.breaker = gobreaker.NewCircuitBreaker(settings)
	return cb, nil
}

func (cb *CircuitBreaker) Execute(operation func() error) error {
	result, err := cb.breaker.Execute(func() (interface{}, error) {
		if err := operation(); err != nil {
			if cb.metrics != nil {
				cb.metrics.failureCount.Inc()
			}
			cb.logger.Debug("Operation failed",
				zap.String("name", cb.name),
				zap.Error(err))
			return nil, err
		}
		return nil, nil
	})

	if err != nil {
		if err == gobreaker.ErrOpenState {
			cb.logger.Debug("Circuit breaker is open",
				zap.String("name", cb.name))
		}
		return err
	}

	_ = result // Ignore result since we don't use it
	return nil
}

func (cb *CircuitBreaker) State() gobreaker.State {
	return cb.breaker.State()
}

func (cb *CircuitBreaker) Counts() gobreaker.Counts {
	return cb.breaker.Counts()
}
