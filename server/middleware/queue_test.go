package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/hapax/server/metrics"
)

func TestQueueMiddleware(t *testing.T) {
	t.Run("basic queue functionality", func(t *testing.T) {
		m := metrics.NewMetrics()
		qm := NewQueueMiddleware(QueueConfig{
			InitialSize: 5,
			Metrics:     m,
		})

		handler := qm.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		// Verify metrics
		queuedRequests := testutil.ToFloat64(m.ActiveRequests.WithLabelValues("queued"))
		assert.Equal(t, float64(0), queuedRequests, "Queue should be empty after request completes")

		processingRequests := testutil.ToFloat64(m.ActiveRequests.WithLabelValues("processing"))
		assert.Equal(t, float64(0), processingRequests, "No requests should be processing after completion")
	})

	t.Run("queue size adjustment", func(t *testing.T) {
		m := metrics.NewMetrics()
		qm := NewQueueMiddleware(QueueConfig{
			InitialSize: 5,
			Metrics:     m,
		})

		// Test setting new max size
		qm.SetMaxSize(10)
		assert.Equal(t, int64(10), qm.GetMaxSize())

		// Fill queue to capacity
		handler := qm.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond) // Increase processing time
			w.WriteHeader(http.StatusOK)
		}))

		// Use WaitGroup to track concurrent requests
		var wg sync.WaitGroup
		wg.Add(15)

		// Send requests concurrently
		for i := 0; i < 15; i++ {
			go func() {
				defer wg.Done()
				req := httptest.NewRequest("GET", "/test", nil)
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
			}()
		}

		// Wait a bit for requests to start processing
		time.Sleep(50 * time.Millisecond)

		// Verify queue metrics
		queueSize := qm.GetQueueSize()
		processing := qm.GetProcessing()
		t.Logf("Queue size: %d, Processing: %d", queueSize, processing)
		assert.True(t, queueSize > 0, "Queue should have pending requests")
		assert.True(t, processing > 0, "Should have requests being processed")

		// Verify Prometheus metrics
		queuedRequests := testutil.ToFloat64(m.ActiveRequests.WithLabelValues("queued"))
		assert.True(t, queuedRequests > 0, "Should have queued requests in metrics")

		processingRequests := testutil.ToFloat64(m.ActiveRequests.WithLabelValues("processing"))
		assert.True(t, processingRequests > 0, "Should have processing requests in metrics")

		// Test queue rejection when full
		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if qm.GetQueueSize() >= int(qm.GetMaxSize()) {
			assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
			// Verify queue drops metric
			queueDrops := testutil.ToFloat64(m.ErrorsTotal.WithLabelValues("queue_full"))
			assert.True(t, queueDrops > 0, "Should have recorded queue drops")
		}

		// Wait for all requests to complete
		wg.Wait()

		// Verify cleanup
		assert.Equal(t, float64(0), testutil.ToFloat64(m.ActiveRequests.WithLabelValues("queued")))
		assert.Equal(t, float64(0), testutil.ToFloat64(m.ActiveRequests.WithLabelValues("processing")))
	})

	t.Run("queue latency tracking", func(t *testing.T) {
		m := metrics.NewMetrics()
		qm := NewQueueMiddleware(QueueConfig{
			InitialSize: 5,
			Metrics:     m,
		})

		handler := qm.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		// Verify latency metric exists
		assert.NotNil(t, m.RequestDuration.WithLabelValues("queue_wait"))
	})

	t.Run("request cancellation handling", func(t *testing.T) {
		m := metrics.NewMetrics()
		qm := NewQueueMiddleware(QueueConfig{
			InitialSize: 5,
			Metrics:     m,
		})

		handler := qm.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))

		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
		rr := httptest.NewRecorder()

		// Start request in goroutine
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel() // Cancel request while it's processing
		}()

		handler.ServeHTTP(rr, req)

		// Verify metrics are cleaned up after cancellation
		time.Sleep(50 * time.Millisecond) // Wait for cleanup
		assert.Equal(t, float64(0), testutil.ToFloat64(m.ActiveRequests.WithLabelValues("queued")))
		assert.Equal(t, float64(0), testutil.ToFloat64(m.ActiveRequests.WithLabelValues("processing")))
	})

	t.Run("concurrent queue size adjustments", func(t *testing.T) {
		m := metrics.NewMetrics()
		qm := NewQueueMiddleware(QueueConfig{
			InitialSize: 10,
			Metrics:     m,
		})

		var wg sync.WaitGroup
		wg.Add(10)

		// Concurrently adjust queue size while processing requests
		for i := 0; i < 10; i++ {
			go func(size int64) {
				defer wg.Done()
				qm.SetMaxSize(size)
				assert.Equal(t, size, qm.GetMaxSize())
			}(int64(i + 1))
		}

		wg.Wait()
	})

	t.Run("stress test with rapid requests", func(t *testing.T) {
		m := metrics.NewMetrics()
		qm := NewQueueMiddleware(QueueConfig{
			InitialSize: 100,
			Metrics:     m,
		})

		handler := qm.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(time.Duration(1) * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))

		var wg sync.WaitGroup
		requestCount := 1000
		wg.Add(requestCount)

		start := time.Now()
		// Send many requests rapidly
		for i := 0; i < requestCount; i++ {
			go func() {
				defer wg.Done()
				req := httptest.NewRequest("GET", "/test", nil)
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
			}()
		}

		wg.Wait()
		duration := time.Since(start)

		t.Logf("Processed %d requests in %v", requestCount, duration)
		assert.Equal(t, float64(0), testutil.ToFloat64(m.ActiveRequests.WithLabelValues("queued")))
		assert.Equal(t, float64(0), testutil.ToFloat64(m.ActiveRequests.WithLabelValues("processing")))
	})

	t.Run("queue position tracking", func(t *testing.T) {
		m := metrics.NewMetrics()
		qm := NewQueueMiddleware(QueueConfig{
			InitialSize: 5,
			Metrics:     m,
		})

		var positions []int
		var mu sync.Mutex

		handler := qm.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pos := r.Context().Value("queue_position").(int)
			mu.Lock()
			positions = append(positions, pos)
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))

		var wg sync.WaitGroup
		wg.Add(5)

		for i := 0; i < 5; i++ {
			go func() {
				defer wg.Done()
				req := httptest.NewRequest("GET", "/test", nil)
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
			}()
		}

		wg.Wait()

		// Verify positions were tracked
		mu.Lock()
		defer mu.Unlock()
		assert.Len(t, positions, 5)
		for _, pos := range positions {
			assert.GreaterOrEqual(t, pos, 0)
			assert.Less(t, pos, 5)
		}
	})
}

func TestQueuePersistence(t *testing.T) {
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "queue.json")

	t.Run("persistence across restarts", func(t *testing.T) {
		m := metrics.NewMetrics()

		// Create first instance with persistence
		qm1 := NewQueueMiddleware(QueueConfig{
			InitialSize:  10,
			Metrics:      m,
			StatePath:    statePath,
			SaveInterval: 100 * time.Millisecond,
		})

		// Change queue size and wait for save
		qm1.SetMaxSize(20)
		time.Sleep(200 * time.Millisecond)

		// Verify state was saved
		data, err := os.ReadFile(statePath)
		require.NoError(t, err)

		var state QueueState
		err = json.Unmarshal(data, &state)
		require.NoError(t, err)
		assert.Equal(t, int64(20), state.MaxSize)

		// Simulate shutdown
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, qm1.Shutdown(ctx))

		// Create second instance that should load the saved state
		qm2 := NewQueueMiddleware(QueueConfig{
			InitialSize:  10, // Different from saved state
			Metrics:      m,
			StatePath:    statePath,
			SaveInterval: 100 * time.Millisecond,
		})

		// Verify state was restored
		assert.Equal(t, int64(20), qm2.GetMaxSize())

		// Cleanup
		require.NoError(t, qm2.Shutdown(context.Background()))
		// Remove state file
		os.Remove(statePath)
	})

	t.Run("persistence with active requests", func(t *testing.T) {
		m := metrics.NewMetrics()
		statePath := filepath.Join(tempDir, "queue_active.json")
		qm := NewQueueMiddleware(QueueConfig{
			InitialSize:  5,
			Metrics:      m,
			StatePath:    statePath,
			SaveInterval: 50 * time.Millisecond,
		})

		// Add some requests to the queue
		handler := qm.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))

		var wg sync.WaitGroup
		wg.Add(3)

		// Start some requests
		for i := 0; i < 3; i++ {
			go func() {
				defer wg.Done()
				req := httptest.NewRequest("GET", "/test", nil)
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
			}()
		}

		// Wait for requests to be queued
		time.Sleep(100 * time.Millisecond)

		// Verify state includes queued requests
		data, err := os.ReadFile(statePath)
		require.NoError(t, err)

		var state QueueState
		err = json.Unmarshal(data, &state)
		require.NoError(t, err)
		assert.True(t, state.QueueLength > 0)

		// Wait for completion and shutdown
		wg.Wait()

		// Wait for state to be updated after completion
		time.Sleep(100 * time.Millisecond)
		require.NoError(t, qm.Shutdown(context.Background()))

		// Wait for final state save
		time.Sleep(50 * time.Millisecond)

		// Verify final state shows empty queue
		data, err = os.ReadFile(statePath)
		require.NoError(t, err)

		err = json.Unmarshal(data, &state)
		require.NoError(t, err)
		assert.Equal(t, 0, state.QueueLength, "Queue should be empty in final state")

		// Remove state file
		os.Remove(statePath)
	})

	t.Run("graceful shutdown with timeout", func(t *testing.T) {
		m := metrics.NewMetrics()
		statePath := filepath.Join(tempDir, "queue_timeout.json")
		qm := NewQueueMiddleware(QueueConfig{
			InitialSize:  5,
			Metrics:      m,
			StatePath:    statePath,
			SaveInterval: 50 * time.Millisecond,
		})

		// Add a long-running request
		handler := qm.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Second)
			w.WriteHeader(http.StatusOK)
		}))

		// Start request
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/test", nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
		}()

		// Wait for request to start
		time.Sleep(100 * time.Millisecond)

		// Try to shutdown with short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := qm.Shutdown(ctx)
		cancel()
		assert.Error(t, err, "Shutdown should timeout")

		// Wait a bit for metrics to be recorded
		time.Sleep(50 * time.Millisecond)

		// Verify metrics were recorded
		shutdownErrors := testutil.ToFloat64(m.ErrorsTotal.WithLabelValues("queue_shutdown_timeout"))
		assert.Greater(t, shutdownErrors, float64(0), "Should have recorded shutdown timeout error")

		// Wait for request to complete before final cleanup
		wg.Wait()

		// Final cleanup with longer timeout
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		require.NoError(t, qm.Shutdown(cleanupCtx))

		// Remove state file
		os.Remove(statePath)
	})

	t.Run("persistence error handling", func(t *testing.T) {
		// Create a directory where the state file should be
		invalidPath := filepath.Join(tempDir, "invalid")
		require.NoError(t, os.Mkdir(invalidPath, 0755))

		m := metrics.NewMetrics()
		qm := NewQueueMiddleware(QueueConfig{
			InitialSize:  5,
			Metrics:      m,
			StatePath:    invalidPath, // This is a directory, not a file
			SaveInterval: 50 * time.Millisecond,
		})

		// Wait for save attempt
		time.Sleep(100 * time.Millisecond)

		// Verify error was recorded in metrics
		persistErrors := testutil.ToFloat64(m.ErrorsTotal.WithLabelValues("queue_persistence"))
		assert.True(t, persistErrors > 0)

		require.NoError(t, qm.Shutdown(context.Background()))

		// Remove test directory
		os.RemoveAll(invalidPath)
	})
}
