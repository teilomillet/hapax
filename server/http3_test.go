package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
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

// generateTestCerts generates temporary TLS certificates for testing
func generateTestCerts(t *testing.T) (certFile, keyFile string) {
	tmpDir := t.TempDir()
	certFile = filepath.Join(tmpDir, "cert.pem")
	keyFile = filepath.Join(tmpDir, "key.pem")

	// Run openssl to generate test certificates
	cmd := fmt.Sprintf("openssl req -x509 -newkey rsa:2048 -keyout %s -out %s -days 1 -nodes -subj '/CN=localhost'",
		keyFile, certFile)

	result := runCommand(t, cmd)
	require.NoError(t, result)

	return certFile, keyFile
}

// simulateNetworkCondition simulates various network conditions using tc (traffic control)
func simulateNetworkCondition(t *testing.T, condition string) func() {
	// Skip if not running as root (required for tc)
	if os.Getuid() != 0 {
		t.Skip("Network condition simulation requires root privileges")
	}

	var cmd string
	switch condition {
	case "packet-loss":
		cmd = "tc qdisc add dev lo root netem loss 5%"
	case "latency":
		cmd = "tc qdisc add dev lo root netem delay 100ms 10ms"
	case "bandwidth":
		cmd = "tc qdisc add dev lo root tbf rate 1mbit burst 32kbit latency 400ms"
	default:
		t.Fatalf("Unknown network condition: %s", condition)
	}

	err := exec.Command("bash", "-c", cmd).Run()
	require.NoError(t, err)

	return func() {
		if err := exec.Command("bash", "-c", "tc qdisc del dev lo root").Run(); err != nil {
			t.Logf("Failed to clean up network condition: %v", err)
		}
	}
}

func TestHTTP3ServerRealistic(t *testing.T) {
	// Generate test certificates
	certFile, keyFile := generateTestCerts(t)
	defer os.Remove(certFile)
	defer os.Remove(keyFile)

	// Create test configuration with realistic timeouts
	logger := zaptest.NewLogger(t)

	// Create mock LLM
	mockLLM := NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return `{"status": "ok"}`, nil
	})

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
				IdleTimeout:                5 * time.Minute, // More realistic idle timeout
				MaxBiStreamsConcurrent:     1000,            // Higher for load testing
				MaxUniStreamsConcurrent:    1000,
				MaxStreamReceiveWindow:     10 * 1024 * 1024, // 10MB
				MaxConnectionReceiveWindow: 25 * 1024 * 1024, // 25MB
			},
		},
		LLM: config.LLMConfig{
			Provider:     "mock",
			Model:        "mock-model",
			SystemPrompt: "You are a test assistant",
			Options: map[string]interface{}{
				"temperature": 0.7,
			},
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Routes: []config.RouteConfig{
			{
				Path:    "/v1/completions",
				Handler: "completion",
				Version: "v1",
			},
			{
				Path:    "/health",
				Handler: "health",
				Version: "v1",
			},
		},
	}

	// Create test watcher
	watcher := newTestConfigWatcher(t, cfg)

	// Create server with mock LLM
	server, err := NewServerWithConfig(watcher, mockLLM, logger)
	require.NoError(t, err)

	// Start server in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	// Wait for server to be fully ready
	time.Sleep(2 * time.Second)

	t.Run("Load Test with Mixed Protocols", func(t *testing.T) {
		var wg sync.WaitGroup
		concurrentUsers := 100
		requestsPerUser := 50
		errors := make(chan error, concurrentUsers*requestsPerUser)

		// Create HTTP/3 client with realistic settings
		transport := &http3.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // Only for testing
			},
			QUICConfig: &quic.Config{
				MaxStreamReceiveWindow:     10 * 1024 * 1024,
				MaxConnectionReceiveWindow: 25 * 1024 * 1024,
				KeepAlivePeriod:            30 * time.Second,
				HandshakeIdleTimeout:       10 * time.Second,
			},
		}
		defer transport.Close()

		http3Client := &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		}

		// Regular HTTP client
		http11Client := &http.Client{
			Timeout: 10 * time.Second,
		}

		wg.Add(concurrentUsers)
		start := time.Now()

		for i := 0; i < concurrentUsers; i++ {
			go func(userID int) {
				defer wg.Done()

				for j := 0; j < requestsPerUser; j++ {
					// Alternate between HTTP/1.1 and HTTP/3
					client := http11Client
					url := fmt.Sprintf("http://localhost:%d/health", cfg.Server.Port)
					if j%2 == 0 {
						client = http3Client
						url = fmt.Sprintf("https://localhost:%d/health", cfg.Server.HTTP3.Port)
					}

					resp, err := client.Get(url)
					if err != nil {
						errors <- fmt.Errorf("user %d, request %d: %w", userID, j, err)
						continue
					}
					if _, err := io.Copy(io.Discard, resp.Body); err != nil {
						t.Logf("Error reading response body: %v", err)
					}
					resp.Body.Close()

					if resp.StatusCode != http.StatusOK {
						errors <- fmt.Errorf("user %d, request %d: status %d", userID, j, resp.StatusCode)
					}

					// Random delay between requests (100ms - 500ms)
					time.Sleep(time.Duration(100+300*float64(j%5)) * time.Millisecond)
				}
			}(i)
		}

		wg.Wait()
		duration := time.Since(start)
		close(errors)

		// Collect and report errors
		var errorCount int
		for err := range errors {
			t.Logf("Error: %v", err)
			errorCount++
		}

		totalRequests := concurrentUsers * requestsPerUser
		successRate := float64(totalRequests-errorCount) / float64(totalRequests) * 100

		t.Logf("Load Test Results:")
		t.Logf("Total Requests: %d", totalRequests)
		t.Logf("Errors: %d", errorCount)
		t.Logf("Success Rate: %.2f%%", successRate)
		t.Logf("Duration: %v", duration)
		t.Logf("Requests/sec: %.2f", float64(totalRequests)/duration.Seconds())

		assert.Greater(t, successRate, 95.0, "Success rate should be above 95%")
	})

	t.Run("Network Conditions", func(t *testing.T) {
		conditions := []string{"packet-loss", "latency", "bandwidth"}
		for _, condition := range conditions {
			t.Run(condition, func(t *testing.T) {
				cleanup := simulateNetworkCondition(t, condition)
				defer cleanup()

				// Test both HTTP/1.1 and HTTP/3 under this condition
				clients := map[string]*http.Client{
					"HTTP/1.1": {
						Timeout: 10 * time.Second,
					},
					"HTTP/3": {
						Transport: &http3.Transport{
							TLSClientConfig: &tls.Config{
								InsecureSkipVerify: true,
							},
						},
						Timeout: 10 * time.Second,
					},
				}

				for protocol, client := range clients {
					t.Run(protocol, func(t *testing.T) {
						url := fmt.Sprintf("http://localhost:%d/health", cfg.Server.Port)
						if protocol == "HTTP/3" {
							url = fmt.Sprintf("https://localhost:%d/health", cfg.Server.HTTP3.Port)
						}

						start := time.Now()
						resp, err := client.Get(url)
						latency := time.Since(start)

						require.NoError(t, err)
						defer resp.Body.Close()
						assert.Equal(t, http.StatusOK, resp.StatusCode)

						t.Logf("%s latency under %s: %v", protocol, condition, latency)
					})
				}
			})
		}
	})

	t.Run("Connection Migration", func(t *testing.T) {
		// Create a connection to the HTTP/3 server
		transport := &http3.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
		defer transport.Close()

		client := &http.Client{
			Transport: transport,
		}

		// Start a long-running request
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET",
			fmt.Sprintf("https://localhost:%d/health", cfg.Server.HTTP3.Port), nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Simulate network interface change
		if os.Getuid() == 0 { // Only run if root
			err = exec.Command("bash", "-c", "ip link set lo down && sleep 1 && ip link set lo up").Run()
			require.NoError(t, err)

			// Verify connection survives
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Contains(t, string(body), "ok")
		} else {
			t.Skip("Connection migration test requires root privileges")
		}
	})

	// Graceful shutdown with active connections
	t.Run("Graceful Shutdown with Load", func(t *testing.T) {
		// Start multiple long-running requests
		var wg sync.WaitGroup
		activeRequests := 50
		wg.Add(activeRequests)

		transport := &http3.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
		defer transport.Close()

		client := &http.Client{
			Transport: transport,
		}

		for i := 0; i < activeRequests; i++ {
			go func() {
				defer wg.Done()
				resp, err := client.Get(fmt.Sprintf("https://localhost:%d/health", cfg.Server.HTTP3.Port))
				if err == nil {
					if _, err := io.Copy(io.Discard, resp.Body); err != nil {
						t.Logf("Error reading response body during shutdown: %v", err)
					}
					resp.Body.Close()
				}
			}()
		}

		// Wait for requests to start
		time.Sleep(1 * time.Second)

		// Trigger graceful shutdown
		shutdownStart := time.Now()
		cancel()

		// Wait for shutdown to complete
		select {
		case err := <-errCh:
			shutdownDuration := time.Since(shutdownStart)
			assert.NoError(t, err)
			t.Logf("Graceful shutdown completed in %v", shutdownDuration)
		case <-time.After(30 * time.Second):
			t.Fatal("Server shutdown timed out")
		}

		// Wait for all requests to complete
		wg.Wait()
	})
}

// Helper types and functions for testing

type testConfigWatcher struct {
	cfg *config.Config
	ch  chan *config.Config
}

func newTestConfigWatcher(t *testing.T, cfg *config.Config) config.Watcher {
	return &testConfigWatcher{
		cfg: cfg,
		ch:  make(chan *config.Config),
	}
}

func (w *testConfigWatcher) GetCurrentConfig() *config.Config {
	return w.cfg
}

func (w *testConfigWatcher) Subscribe() <-chan *config.Config {
	return w.ch
}

func (w *testConfigWatcher) Close() error {
	close(w.ch)
	return nil
}

func runCommand(t *testing.T, cmd string) error {
	// Implementation depends on your testing environment
	// For now, we'll use os.exec
	result := exec.Command("bash", "-c", cmd).Run()
	return result
}
