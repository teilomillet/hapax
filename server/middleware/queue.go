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

// QueueState represents the persistent state of the queue
type QueueState struct {
	MaxSize     int64     `json:"max_size"`
	QueueLength int       `json:"queue_length"`
	LastSaved   time.Time `json:"last_saved"`
}

type QueueMiddleware struct {
	queue         *queue.Queue[chan struct{}]
	maxSize       atomic.Int64
	mu            sync.RWMutex
	processing    int32
	metrics       *metrics.Metrics
	statePath     string
	persistTicker *time.Ticker
	done          chan struct{}
}

type QueueConfig struct {
	InitialSize  int64
	Metrics      *metrics.Metrics
	StatePath    string        // Path to store queue state
	SaveInterval time.Duration // How often to save state (0 means no persistence)
}

func NewQueueMiddleware(cfg QueueConfig) *QueueMiddleware {
	qm := &QueueMiddleware{
		queue:     queue.New[chan struct{}](),
		metrics:   cfg.Metrics,
		statePath: cfg.StatePath,
		done:      make(chan struct{}),
	}

	// Try to load previous state
	if cfg.StatePath != "" {
		if err := qm.loadState(); err != nil {
			// If no state exists, use initial size
			qm.maxSize.Store(cfg.InitialSize)
		}

		// Start persistence goroutine if save interval is specified
		if cfg.SaveInterval > 0 {
			qm.persistTicker = time.NewTicker(cfg.SaveInterval)
			go qm.persistStateRoutine()
		}
	} else {
		qm.maxSize.Store(cfg.InitialSize)
	}

	return qm
}

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

func (qm *QueueMiddleware) SetMaxSize(size int64) {
	qm.maxSize.Store(size)
	// Save state after size change
	if qm.statePath != "" {
		go qm.saveState() // Non-blocking save
	}
}

func (qm *QueueMiddleware) GetQueueSize() int {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	return qm.queue.Length()
}

func (qm *QueueMiddleware) GetMaxSize() int64 {
	return qm.maxSize.Load()
}

func (qm *QueueMiddleware) GetProcessing() int32 {
	return atomic.LoadInt32(&qm.processing)
}

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

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), "queue_position", currentSize)))
	})
}
