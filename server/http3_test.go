package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/mocks"
	"go.uber.org/zap/zaptest"
)

// generateTestCerts generates temporary TLS certificates for testing
func generateTestCerts(t *testing.T) (certFile, keyFile string) {
	tmpDir := t.TempDir()
	certFile = filepath.Join(tmpDir, "cert.pem")
	keyFile = filepath.Join(tmpDir, "key.pem")

	cmd := fmt.Sprintf("openssl req -x509 -newkey rsa:2048 -keyout %s -out %s -days 1 -nodes -subj '/CN=localhost'",
		keyFile, certFile)

	result := runCommand(t, cmd)
	require.NoError(t, result)

	return certFile, keyFile
}

func runCommand(t *testing.T, cmd string) error {
	return exec.Command("bash", "-c", cmd).Run()
}

func TestHTTP3ServerRealistic(t *testing.T) {
	// Helper function to check if port is in use
	portInUse := func(port int) bool {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			return true
		}
		ln.Close()
		return false
	}

	// Helper function to wait for port to be available
	waitForPortAvailable := func(port int) error {
		for i := 0; i < 50; i++ { // Try for 5 seconds (50 * 100ms)
			if !portInUse(port) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("port %d is still in use after timeout", port)
	}

	// Wait for ports to be available
	ports := []int{9090, 9443}
	for _, port := range ports {
		require.NoError(t, waitForPortAvailable(port), "Port %d is still in use", port)
	}

	// Generate test certificates
	certFile, keyFile := generateTestCerts(t)
	defer os.Remove(certFile)
	defer os.Remove(keyFile)

	// Create test configuration with realistic timeouts
	logger := zaptest.NewLogger(t)

	// Create a mock LLM
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "test response", nil
	})

	// Create a configuration with a smaller UDP buffer size for testing
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:            9090,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			MaxHeaderBytes:  1 << 20,
			ShutdownTimeout: 5 * time.Second, // Use a shorter timeout for testing
			HTTP3: &config.HTTP3Config{
				Enabled:                    true,
				Port:                       9443,
				TLSCertFile:                certFile,
				TLSKeyFile:                 keyFile,
				IdleTimeout:                30 * time.Second,
				MaxBiStreamsConcurrent:     100,
				MaxUniStreamsConcurrent:    100,
				MaxStreamReceiveWindow:     1 * 1024 * 1024, // 1MB
				MaxConnectionReceiveWindow: 2 * 1024 * 1024, // 2MB
				UDPReceiveBufferSize:       416 * 1024,      // Use the actual size we can get (416KB)
			},
		},
		LLM: config.LLMConfig{
			Provider: "mock",
			Model:    "test",
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
				Path:    "/health",
				Handler: "health",
				Version: "v1",
			},
		},
	}

	// Create and start server
	watcher := NewMockConfigWatcher(cfg)
	server, err := NewServerWithConfig(watcher, mockLLM, logger)
	require.NoError(t, err)

	// Create a context with timeout for the entire test
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start server in goroutine
	serverErrChan := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			serverErrChan <- err
		}
		close(serverErrChan)
	}()

	// Wait for server to be ready
	require.Eventually(t, func() bool {
		// Try to connect to the HTTP/3 endpoint
		client := &http.Client{
			Transport: &http3.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
		resp, err := client.Get(fmt.Sprintf("https://localhost:%d/health", cfg.Server.HTTP3.Port))
		if err != nil {
			t.Logf("HTTP/3 connection attempt failed: %v", err)
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 100*time.Millisecond, "Server failed to start")

	// Run tests
	t.Run("Basic Health Check", func(t *testing.T) {
		client := &http.Client{
			Transport: &http3.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
		resp, err := client.Get(fmt.Sprintf("https://localhost:%d/health", cfg.Server.HTTP3.Port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	// Cleanup
	cancel()

	// Wait for server to shut down
	select {
	case err := <-serverErrChan:
		if err != nil && err != context.Canceled {
			require.NoError(t, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Server failed to shut down")
	}

	// Wait for ports to be released
	for _, port := range ports {
		require.NoError(t, waitForPortAvailable(port), "Port %d was not released after server shutdown", port)
	}
}

func TestHTTP3_ZeroRTT(t *testing.T) {
	// Helper function to check if port is in use
	portInUse := func(port int) bool {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			return true
		}
		ln.Close()
		return false
	}

	// Helper function to wait for port to be available
	waitForPortAvailable := func(port int) error {
		for i := 0; i < 50; i++ { // Try for 5 seconds (50 * 100ms)
			if !portInUse(port) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("port %d is still in use after timeout", port)
	}

	// Wait for ports to be available
	ports := []int{9090, 9443}
	for _, port := range ports {
		require.NoError(t, waitForPortAvailable(port), "Port %d is still in use", port)
	}

	// Generate test certificates
	certFile, keyFile := generateTestCerts(t)
	defer os.Remove(certFile)
	defer os.Remove(keyFile)

	// Create test configuration with realistic timeouts
	logger := zaptest.NewLogger(t)

	// Create a mock LLM
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "test response", nil
	})

	// Create a configuration with 0-RTT enabled
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:            9090,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			MaxHeaderBytes:  1 << 20,
			ShutdownTimeout: 5 * time.Second, // Use a shorter timeout for testing
			HTTP3: &config.HTTP3Config{
				Enabled:                    true,
				Port:                       9443,
				TLSCertFile:                certFile,
				TLSKeyFile:                 keyFile,
				IdleTimeout:                30 * time.Second,
				MaxBiStreamsConcurrent:     100,
				MaxUniStreamsConcurrent:    100,
				MaxStreamReceiveWindow:     1 * 1024 * 1024, // 1MB
				MaxConnectionReceiveWindow: 2 * 1024 * 1024, // 2MB
				UDPReceiveBufferSize:       416 * 1024,      // Use the actual size we can get (416KB)
				Enable0RTT:                 true,            // Enable 0-RTT
			},
		},
		LLM: config.LLMConfig{
			Provider: "mock",
			Model:    "test",
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
				Path:    "/health",
				Handler: "health",
				Version: "v1",
			},
		},
	}

	// Create and start server
	watcher := NewMockConfigWatcher(cfg)
	server, err := NewServerWithConfig(watcher, mockLLM, logger)
	require.NoError(t, err)

	// Create a context with timeout for the entire test
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start server in goroutine
	serverErrChan := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			serverErrChan <- err
		}
		close(serverErrChan)
	}()

	// Create a client that supports 0-RTT
	client := &http.Client{
		Transport: &http3.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			QUICConfig: &quic.Config{
				Allow0RTT: true,
			},
		},
	}

	// Wait for server to be ready
	require.Eventually(t, func() bool {
		resp, err := client.Get(fmt.Sprintf("https://localhost:%d/health", cfg.Server.HTTP3.Port))
		if err != nil {
			t.Logf("HTTP/3 connection attempt failed: %v", err)
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 100*time.Millisecond, "Server failed to start")

	// Make a second request to test 0-RTT
	resp, err := client.Get(fmt.Sprintf("https://localhost:%d/health", cfg.Server.HTTP3.Port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Cleanup
	cancel()

	// Wait for server to shut down
	select {
	case err := <-serverErrChan:
		if err != nil && err != context.Canceled {
			require.NoError(t, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Server failed to shut down")
	}

	// Wait for ports to be released
	for _, port := range ports {
		require.NoError(t, waitForPortAvailable(port), "Port %d was not released after server shutdown", port)
	}
}
