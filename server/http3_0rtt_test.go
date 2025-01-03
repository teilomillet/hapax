package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"go.uber.org/zap/zaptest"
)

func TestHTTP3_0RTT(t *testing.T) {
	// Generate test certificates
	certFile, keyFile := generateTestCerts(t)
	defer cleanup(t, certFile, keyFile)

	logger := zaptest.NewLogger(t)

	// Create mock LLM with realistic response delay
	mockLLM := NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		// Simulate realistic API latency
		time.Sleep(50 * time.Millisecond)
		return `{"status": "ok", "latency": 50}`, nil
	})

	// Create configuration with 0-RTT enabled
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:            8080,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			MaxHeaderBytes:  1 << 20,
			ShutdownTimeout: 30 * time.Second,
			HTTP3: &config.HTTP3Config{
				Enabled:                    true,
				Port:                       8443,
				TLSCertFile:                certFile,
				TLSKeyFile:                 keyFile,
				IdleTimeout:                5 * time.Minute,
				MaxBiStreamsConcurrent:     100,
				MaxUniStreamsConcurrent:    100,
				MaxStreamReceiveWindow:     6 * 1024 * 1024,
				MaxConnectionReceiveWindow: 15 * 1024 * 1024,
				Enable0RTT:                 true,
				Max0RTTSize:                16 * 1024,
				Allow0RTTReplay:            false,
			},
		},
		LLM: config.LLMConfig{
			Provider:     "mock",
			Model:        "mock-model",
			SystemPrompt: "You are a test assistant",
		},
	}

	// Create test watcher
	watcher := newTestConfigWatcher(t, cfg)

	// Create and start server
	server, err := NewServerWithConfig(watcher, mockLLM, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	// Wait for server to be ready
	time.Sleep(2 * time.Second)

	t.Run("0-RTT Basic Functionality", func(t *testing.T) {
		transport := &http3.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				ClientSessionCache: tls.NewLRUClientSessionCache(10),
			},
			QUICConfig: &quic.Config{
				Allow0RTT: true,
			},
		}
		defer transport.Close()

		client := &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		}

		url := fmt.Sprintf("https://localhost:%d/health", cfg.Server.HTTP3.Port)

		// First request - should establish session
		start := time.Now()
		resp, err := client.Get(url)
		firstLatency := time.Since(start)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		resp.Body.Close()

		t.Logf("First request (with handshake) latency: %v", firstLatency)

		// Wait for session ticket to be processed
		time.Sleep(200 * time.Millisecond)

		// Second request - should use 0-RTT if enabled
		start = time.Now()
		resp, err = client.Get(url)
		secondLatency := time.Since(start)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		resp.Body.Close()

		t.Logf("Second request (potential 0-RTT) latency: %v", secondLatency)

		// Verify 0-RTT improvement
		assert.Less(t, secondLatency, firstLatency, "0-RTT request should be faster than initial handshake")
	})

	t.Run("0-RTT Replay Protection with Real Data", func(t *testing.T) {
		if cfg.Server.HTTP3.Allow0RTTReplay {
			t.Skip("Test only relevant when replay protection is enabled")
		}

		transport := &http3.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				ClientSessionCache: tls.NewLRUClientSessionCache(10),
			},
			QUICConfig: &quic.Config{
				Allow0RTT: true,
			},
		}
		defer transport.Close()

		client := &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		}

		// Create a POST request with valid completion data
		url := fmt.Sprintf("https://localhost:%d/v1/completions", cfg.Server.HTTP3.Port)
		completionData := []byte(`{
			"input": "What is the meaning of life?",
			"temperature": 0.7,
			"messages": [
				{"role": "user", "content": "What is the meaning of life?"}
			]
		}`)

		// First request to establish session
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(completionData))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		resp.Body.Close()

		// Wait for session ticket and first request to be fully processed
		time.Sleep(200 * time.Millisecond)

		// Try to replay the same request multiple times
		replayAttempts := 5
		results := make(chan struct {
			statusCode int
			latency    time.Duration
		}, replayAttempts)

		// Process replays sequentially to ensure deterministic behavior
		for i := 0; i < replayAttempts; i++ {
			start := time.Now()
			req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(completionData))
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			latency := time.Since(start)

			if err != nil {
				t.Logf("Request error: %v", err)
				results <- struct {
					statusCode int
					latency    time.Duration
				}{0, latency}
				continue
			}
			_, err = io.Copy(io.Discard, resp.Body)
			require.NoError(t, err)
			resp.Body.Close()
			results <- struct {
				statusCode int
				latency    time.Duration
			}{resp.StatusCode, latency}

			// Small delay between attempts
			time.Sleep(10 * time.Millisecond)
		}

		close(results)

		// Analyze results
		var successCount int
		var rejectedCount int
		var totalLatency time.Duration
		for result := range results {
			if result.statusCode == http.StatusOK {
				successCount++
				totalLatency += result.latency
			} else if result.statusCode == http.StatusTooEarly {
				rejectedCount++
			}
		}

		// First request should succeed, rest should be rejected
		assert.Equal(t, 0, successCount, "All replay attempts should be rejected")
		assert.Equal(t, replayAttempts, rejectedCount, "All replay attempts should return 425 Too Early")

		if successCount > 0 {
			avgLatency := totalLatency / time.Duration(successCount)
			t.Logf("Average latency for successful requests: %v", avgLatency)
			assert.Greater(t, avgLatency, 50*time.Millisecond, "Successful request should include API latency")
		}
	})
}

func cleanup(t *testing.T, files ...string) {
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			t.Logf("Failed to remove file %s: %v", file, err)
		}
	}
}
