package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
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
	"github.com/teilomillet/hapax/server/mocks"
	"go.uber.org/zap/zaptest"
)

func generateTestCertificates(t *testing.T) (string, string) {
	certFile, err := os.CreateTemp("", "cert*.pem")
	require.NoError(t, err)
	keyFile, err := os.CreateTemp("", "key*.pem")
	require.NoError(t, err)

	// Generate self-signed certificate
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Co"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour * 24 * 180),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	require.NoError(t, err)

	// Write certificate
	err = pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	require.NoError(t, err)

	// Write private key
	privBytes := x509.MarshalPKCS1PrivateKey(priv)
	err = pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes})
	require.NoError(t, err)

	certFile.Close()
	keyFile.Close()

	return certFile.Name(), keyFile.Name()
}

func TestHTTP3_0RTT(t *testing.T) {
	// HTTP/3 (QUIC) requires specific UDP buffer sizes to function properly.
	// The quic-go library needs at least 7MB (7168 KB) for optimal performance.
	// Most CI environments have restricted UDP buffer sizes (typically 2MB max),
	// making it impossible to properly test HTTP/3 0-RTT functionality.
	//
	// See: https://github.com/quic-go/quic-go/wiki/UDP-Buffer-Sizes
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping HTTP/3 0-RTT test in CI environment due to UDP buffer size limitations (needs 7MB, CI typically allows only 2MB)")
	}

	// Create test certificates
	certFile, keyFile := generateTestCertificates(t)
	defer os.Remove(certFile)
	defer os.Remove(keyFile)

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
				MaxBiStreamsConcurrent:     1000,
				MaxUniStreamsConcurrent:    1000,
				MaxStreamReceiveWindow:     10 * 1024 * 1024,
				MaxConnectionReceiveWindow: 25 * 1024 * 1024,
				Enable0RTT:                 true,
				Max0RTTSize:                16 * 1024,
				Allow0RTTReplay:            false,
				// Set UDP buffer size to 7MB as required by quic-go for proper operation
				// This value comes from quic-go's internal requirements:
				// https://github.com/quic-go/quic-go/wiki/UDP-Buffer-Sizes#non-bsd
				UDPReceiveBufferSize: 7168 * 1024, // 7MB (7168 KB) - minimum required by quic-go
			},
		},
		LLM: config.LLMConfig{
			Provider:     "mock",
			Model:        "mock-model",
			SystemPrompt: "You are a test assistant",
		},
	}

	// Create test logger
	logger := zaptest.NewLogger(t)

	// Create mock LLM
	mockLLM := mocks.NewMockLLM(func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
		return "test response", nil
	})

	// Create server with better error handling
	server, err := NewServerWithConfig(mocks.NewMockConfigWatcher(cfg), mockLLM, logger)
	require.NoError(t, err, "Failed to create server")
	require.NotNil(t, server, "Server instance should not be nil")

	// Start server
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	// Configure HTTP/3 client with longer timeouts
	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		QUICConfig: &quic.Config{
			MaxIdleTimeout:             30 * time.Second,
			HandshakeIdleTimeout:       10 * time.Second,
			MaxStreamReceiveWindow:     10 * 1024 * 1024,
			MaxConnectionReceiveWindow: 25 * 1024 * 1024,
			KeepAlivePeriod:            5 * time.Second,
			Allow0RTT:                  true,
		},
	}
	defer transport.Close()

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	// Wait for server to be ready
	require.Eventually(t, func() bool {
		resp, err := client.Get("https://localhost:8443/health")
		if err != nil {
			t.Logf("Server not ready: %v", err)
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 100*time.Millisecond, "Server failed to start")

	t.Run("0-RTT Basic Functionality", func(t *testing.T) {
		// First request establishes connection
		resp, err := client.Get("https://localhost:8443/health")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Second request should use 0-RTT
		resp, err = client.Get("https://localhost:8443/health")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("0-RTT Replay Protection with Real Data", func(t *testing.T) {
		// Create completion request
		reqBody := map[string]string{"input": "test"}
		jsonData, err := json.Marshal(reqBody)
		require.NoError(t, err)

		// First request
		req1, err := http.NewRequest(http.MethodPost, "https://localhost:8443/v1/completions", bytes.NewBuffer(jsonData))
		require.NoError(t, err)
		req1.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req1)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Create a new request with the same data for replay
		req2, err := http.NewRequest(http.MethodPost, "https://localhost:8443/v1/completions", bytes.NewBuffer(jsonData))
		require.NoError(t, err)
		req2.Header.Set("Content-Type", "application/json")

		// Immediate replay should be rejected
		resp, err = client.Do(req2)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusTooEarly, resp.StatusCode)
	})

	// Cleanup
	cancel()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Error("Server did not shut down within timeout")
	}
}
