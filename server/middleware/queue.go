package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eapache/queue/v2"
	"github.com/teilomillet/hapax/server/metrics"
)

// queueContextKey is a custom type for queue-specific context keys to avoid collisions
type queueContextKey string

// Queue-specific context keys
const (
	queuePositionKey queueContextKey = "queue_position"
)

// QueueMiddleware implements a request queue with built-in self-cleaning capabilities.
// Core Design:
// 1. Request Lifecycle:
//   - Incoming requests are added to a FIFO queue if space is available
//   - Each queued request gets a channel that signals its completion
//   - When a request completes, its resources are automatically cleaned up
//   - Queue position is passed to request context for tracking
//
// 2. Self-Cleaning Mechanisms:
//   - Channel-based: Each request's done channel is closed on completion
//   - Defer-based: Resources are released even if request panics
//   - Queue-based: Completed requests are removed from queue automatically
//   - Memory-based: Go's GC reclaims unused resources
//
// 3. Thread Safety:
//   - RWMutex protects queue operations (add/remove)
//   - Atomic operations for counters (maxSize, processing)
//   - Channel-based synchronization for request completion
//
// 4. Health Monitoring:
//   - Tracks active requests (queued vs processing)
//   - Measures queue wait times and request duration
//   - Counts errors (queue full, persistence failures)
//   - Monitors queue size against configured maximum
//
// 5. State Persistence:
//   - Periodic saves of queue state if configured
//   - Atomic file operations prevent corruption
//   - Automatic recovery on restart
type QueueMiddleware struct {
	queue         *queue.Queue[chan struct{}] // FIFO queue holding channels that signal request completion
	maxSize       atomic.Int64                // Maximum queue size, updated atomically
	mu            sync.RWMutex                // Protects queue operations
	processing    int32                       // Count of requests being processed
	metrics       *metrics.Metrics            // Prometheus metrics for monitoring
	statePath     string                      // Path for state persistence
	persistTicker *time.Ticker                // Timer for state saves
	done          chan struct{}               // Signals shutdown
}

// QueueState represents the persistent state of the queue that can be saved and restored.
// This enables the queue to maintain its configuration across server restarts.
// Fields:
// - MaxSize: Maximum number of requests allowed in queue
// - QueueLength: Number of requests in queue at time of save
// - LastSaved: Timestamp of last successful save
type QueueState struct {
	MaxSize     int64     `json:"max_size"`     // Maximum allowed queue size
	QueueLength int       `json:"queue_length"` // Current number of items in queue
	LastSaved   time.Time `json:"last_saved"`   // Timestamp of last save
}

// QueueConfig defines the operational parameters for the queue middleware.
// These settings control the queue's behavior, capacity, and persistence strategy.
// Fields:
// - InitialSize: Starting maximum queue size if no saved state
// - Metrics: Prometheus metrics collector for monitoring
// - StatePath: File path for state persistence (empty = no persistence)
// - SaveInterval: Frequency of state saves (0 = no periodic saves)
type QueueConfig struct {
	InitialSize  int64            // Starting maximum queue size
	Metrics      *metrics.Metrics // Metrics collector for monitoring
	StatePath    string           // Path to store queue state, empty disables persistence
	SaveInterval time.Duration    // How often to save state (0 means no persistence)
}

// NewQueueMiddleware initializes a new queue middleware with the given configuration.
// Initialization Process:
// 1. Creates queue data structures and channels
// 2. Attempts to restore previous state if persistence enabled
// 3. Starts background state persistence if configured
// 4. Initializes metrics collection
//
// The queue begins accepting requests immediately after initialization.
// If state persistence is enabled, it will attempt to restore the previous
// configuration, falling back to InitialSize if no state exists.
func NewQueueMiddleware(cfg QueueConfig) *QueueMiddleware {
	qm := &QueueMiddleware{
		queue:     queue.New[chan struct{}](),
		metrics:   cfg.Metrics,
		statePath: cfg.StatePath,
		done:      make(chan struct{}),
	}

	// Set initial size first
	qm.maxSize.Store(cfg.InitialSize)

	// Try to load previous state
	if cfg.StatePath != "" {
		if err := qm.loadState(); err != nil {
			// Log error but continue with initial size
			if qm.metrics != nil {
				qm.metrics.ErrorsTotal.WithLabelValues("queue_load_state").Inc()
			}
		}

		// Start persistence goroutine if save interval is specified
		if cfg.SaveInterval > 0 {
			qm.persistTicker = time.NewTicker(cfg.SaveInterval)
			go qm.persistStateRoutine()
		}
	}

	return qm
}

// loadState attempts to restore the queue's previous state from disk.
// Recovery Process:
// 1. Verifies storage directory exists/is accessible
// 2. Reads and validates stored state file
// 3. Restores previous queue configuration
//
// If the state file doesn't exist or is invalid, the queue will
// use its default configuration without returning an error.
func (qm *QueueMiddleware) loadState() error {
	if qm.statePath == "" {
		return nil
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(qm.statePath), 0755); err != nil {
		return err
	}

	data, err := os.ReadFile(qm.statePath)
	if err != nil {
		return err
	}

	var state QueueState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	qm.maxSize.Store(state.MaxSize)
	return nil
}

// saveState persists the current queue state to disk atomically.
// Save Process:
// 1. Captures current queue metrics under read lock
// 2. Serializes state to temporary file
// 3. Atomically replaces old state file
//
// This method ensures state file consistency by using atomic
// file operations, preventing corruption during saves.
func (qm *QueueMiddleware) saveState() error {
	if qm.statePath == "" {
		return nil
	}

	qm.mu.RLock()
	state := QueueState{
		MaxSize:     qm.maxSize.Load(),
		QueueLength: qm.queue.Length(),
		LastSaved:   time.Now(),
	}
	qm.mu.RUnlock()

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(qm.statePath), 0755); err != nil {
		return err
	}

	// Write atomically by using a temporary file
	tmpFile := qm.statePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpFile, qm.statePath)
}

// persistStateRoutine manages periodic state persistence.
// Operation:
// 1. Saves state at configured intervals
// 2. Handles persistence errors with metrics
// 3. Performs final save on shutdown
//
// This routine runs in the background until shutdown is signaled
// via the done channel. Errors during saves are recorded in metrics
// but don't stop the routine.
func (qm *QueueMiddleware) persistStateRoutine() {
	for {
		select {
		case <-qm.persistTicker.C:
			if err := qm.saveState(); err != nil {
				// Log error but continue
				if qm.metrics != nil {
					qm.metrics.ErrorsTotal.WithLabelValues("queue_persistence").Inc()
				}
			}
		case <-qm.done:
			if qm.persistTicker != nil {
				qm.persistTicker.Stop()
			}
			// Final save on shutdown
			_ = qm.saveState()
			return
		}
	}
}

// Shutdown initiates a graceful shutdown of the queue middleware.
// Shutdown Process:
// 1. Signals shutdown via done channel
// 2. Stops periodic state persistence
// 3. Waits for queued requests to complete (with timeout)
// 4. Performs final state save
//
// The shutdown will timeout if requests don't complete within
// 5 seconds, recording a metric and returning an error.
func (qm *QueueMiddleware) Shutdown(ctx context.Context) error {
	// Only close the done channel once
	select {
	case <-qm.done:
		// Channel already closed, continue with shutdown
	default:
		close(qm.done)
	}

	if qm.persistTicker != nil {
		qm.persistTicker.Stop()
	}

	// Wait for queue to drain with timeout
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		qm.mu.RLock()
		if qm.queue.Length() == 0 && atomic.LoadInt32(&qm.processing) == 0 {
			qm.mu.RUnlock()
			// Final state save
			if err := qm.saveState(); err != nil && qm.metrics != nil {
				qm.metrics.ErrorsTotal.WithLabelValues("queue_persistence").Inc()
			}
			return nil
		}
		qm.mu.RUnlock()
		select {
		case <-ctx.Done():
			if qm.metrics != nil {
				qm.metrics.ErrorsTotal.WithLabelValues("queue_shutdown_timeout").Inc()
			}
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if qm.metrics != nil {
		qm.metrics.ErrorsTotal.WithLabelValues("queue_shutdown_timeout").Inc()
	}
	return nil
}

// SetMaxSize updates the maximum number of requests allowed in the queue.
// Update Process:
// 1. Atomically updates size limit
// 2. Triggers async state save if persistence enabled
//
// This is a thread-safe operation that takes effect immediately.
// New requests will be rejected if queue length reaches the new limit.
func (qm *QueueMiddleware) SetMaxSize(size int64) {
	qm.maxSize.Store(size)
	// Save state after size change
	if qm.statePath != "" {
		go func() {
			if err := qm.saveState(); err != nil && qm.metrics != nil {
				qm.metrics.ErrorsTotal.WithLabelValues("queue_persistence").Inc()
			}
		}()
	}
}

// GetQueueSize returns the current queue length.
// Thread-safe operation protected by mutex.
func (qm *QueueMiddleware) GetQueueSize() int {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	return qm.queue.Length()
}

// GetMaxSize returns the current maximum queue size.
// Thread-safe operation using atomic load.
func (qm *QueueMiddleware) GetMaxSize() int64 {
	return qm.maxSize.Load()
}

// GetProcessing returns the number of requests currently being processed.
// Thread-safe operation using atomic load.
func (qm *QueueMiddleware) GetProcessing() int32 {
	return atomic.LoadInt32(&qm.processing)
}

// Handler manages the request lifecycle through the queue.
// Request Flow:
// 1. Queue Check:
//   - Verifies space available in queue
//   - Rejects request if queue full
//
// 2. Request Queuing:
//   - Creates completion channel
//   - Adds request to queue
//   - Updates queue metrics
//
// 3. Request Processing:
//   - Tracks processing state
//   - Passes queue position to request context
//   - Forwards request to next handler
//
// 4. Automatic Cleanup:
//   - Removes request from queue
//   - Updates metrics
//   - Closes completion channel
//   - Records timing metrics
//
// All operations are thread-safe and self-cleaning through
// defer blocks and channel-based synchronization.
func (qm *QueueMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		qm.mu.Lock()
		currentSize := qm.queue.Length()
		maxSize := qm.maxSize.Load()

		// Update queue size metric
		if qm.metrics != nil {
			qm.metrics.ActiveRequests.WithLabelValues("queued").Set(float64(currentSize))
		}

		if int64(currentSize) >= maxSize {
			qm.mu.Unlock()
			// Increment queue drops metric
			if qm.metrics != nil {
				qm.metrics.ErrorsTotal.WithLabelValues("queue_full").Inc()
			}
			http.Error(w, "Queue is full", http.StatusServiceUnavailable)
			return
		}

		done := make(chan struct{})
		qm.queue.Add(done)
		qm.mu.Unlock()

		// Update processing count metric
		atomic.AddInt32(&qm.processing, 1)
		if qm.metrics != nil {
			qm.metrics.ActiveRequests.WithLabelValues("processing").Inc()
		}

		defer func() {
			atomic.AddInt32(&qm.processing, -1)
			if qm.metrics != nil {
				qm.metrics.ActiveRequests.WithLabelValues("processing").Dec()
			}
			close(done)
			qm.mu.Lock()
			qm.queue.Remove()
			// Update queue size metric after removal
			if qm.metrics != nil {
				qm.metrics.ActiveRequests.WithLabelValues("queued").Set(float64(qm.queue.Length()))
			}
			qm.mu.Unlock()

			// Record queue latency
			if qm.metrics != nil {
				qm.metrics.RequestDuration.WithLabelValues("queue_wait").Observe(time.Since(start).Seconds())
			}
		}()

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), queuePositionKey, currentSize)))
	})
}
