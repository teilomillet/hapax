package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTP3Config(t *testing.T) {
	// Create temporary directory for test certificates
	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")

	// Create dummy certificate files
	require.NoError(t, os.WriteFile(certFile, []byte("test cert"), 0644))
	require.NoError(t, os.WriteFile(keyFile, []byte("test key"), 0644))

	// Base LLM config that all tests will use
	baseLLMConfig := LLMConfig{
		Provider: "mock",
		Model:    "mock-model",
		Options: map[string]interface{}{
			"temperature": 0.7,
		},
		SystemPrompt: "You are a test assistant",
	}

	tests := []struct {
		name        string
		config      *Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid HTTP/3 config",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					MaxHeaderBytes:  1 << 20,
					ShutdownTimeout: 30 * time.Second,
					HTTP3: &HTTP3Config{
						Enabled:                    true,
						Port:                       443,
						TLSCertFile:                certFile,
						TLSKeyFile:                 keyFile,
						IdleTimeout:                30 * time.Second,
						MaxBiStreamsConcurrent:     100,
						MaxUniStreamsConcurrent:    100,
						MaxStreamReceiveWindow:     6 * 1024 * 1024,
						MaxConnectionReceiveWindow: 15 * 1024 * 1024,
					},
				},
				LLM: baseLLMConfig,
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			expectError: false,
		},
		{
			name: "HTTP/3 disabled",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					MaxHeaderBytes:  1 << 20,
					ShutdownTimeout: 30 * time.Second,
					HTTP3: &HTTP3Config{
						Enabled: false,
					},
				},
				LLM: baseLLMConfig,
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			expectError: false,
		},
		{
			name: "invalid port",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					MaxHeaderBytes:  1 << 20,
					ShutdownTimeout: 30 * time.Second,
					HTTP3: &HTTP3Config{
						Enabled: true,
						Port:    -1,
					},
				},
				LLM: baseLLMConfig,
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			expectError: true,
			errorMsg:    "invalid HTTP/3 port: -1",
		},
		{
			name: "missing TLS cert",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					MaxHeaderBytes:  1 << 20,
					ShutdownTimeout: 30 * time.Second,
					HTTP3: &HTTP3Config{
						Enabled:    true,
						Port:       443,
						TLSKeyFile: keyFile,
					},
				},
				LLM: baseLLMConfig,
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			expectError: true,
			errorMsg:    "HTTP/3 enabled but TLS certificate file not specified",
		},
		{
			name: "missing TLS key",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					MaxHeaderBytes:  1 << 20,
					ShutdownTimeout: 30 * time.Second,
					HTTP3: &HTTP3Config{
						Enabled:     true,
						Port:        443,
						TLSCertFile: certFile,
					},
				},
				LLM: baseLLMConfig,
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			expectError: true,
			errorMsg:    "HTTP/3 enabled but TLS key file not specified",
		},
		{
			name: "negative idle timeout",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					MaxHeaderBytes:  1 << 20,
					ShutdownTimeout: 30 * time.Second,
					HTTP3: &HTTP3Config{
						Enabled:     true,
						Port:        443,
						TLSCertFile: certFile,
						TLSKeyFile:  keyFile,
						IdleTimeout: -1 * time.Second,
					},
				},
				LLM: baseLLMConfig,
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			expectError: true,
			errorMsg:    "negative HTTP/3 idle timeout",
		},
		{
			name: "negative max streams",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					MaxHeaderBytes:  1 << 20,
					ShutdownTimeout: 30 * time.Second,
					HTTP3: &HTTP3Config{
						Enabled:                true,
						Port:                   443,
						TLSCertFile:            certFile,
						TLSKeyFile:             keyFile,
						IdleTimeout:            30 * time.Second,
						MaxBiStreamsConcurrent: -1,
					},
				},
				LLM: baseLLMConfig,
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			expectError: true,
			errorMsg:    "negative HTTP/3 max bidirectional streams",
		},
		{
			name: "zero window size",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					MaxHeaderBytes:  1 << 20,
					ShutdownTimeout: 30 * time.Second,
					HTTP3: &HTTP3Config{
						Enabled:                    true,
						Port:                       443,
						TLSCertFile:                certFile,
						TLSKeyFile:                 keyFile,
						IdleTimeout:                30 * time.Second,
						MaxBiStreamsConcurrent:     100,
						MaxStreamReceiveWindow:     0,
						MaxConnectionReceiveWindow: 0,
					},
				},
				LLM: baseLLMConfig,
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			expectError: true,
			errorMsg:    "HTTP/3 max stream receive window must be positive",
		},
		{
			name: "inaccessible cert file",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					MaxHeaderBytes:  1 << 20,
					ShutdownTimeout: 30 * time.Second,
					HTTP3: &HTTP3Config{
						Enabled:                    true,
						Port:                       443,
						TLSCertFile:                "/nonexistent/cert.pem",
						TLSKeyFile:                 keyFile,
						IdleTimeout:                30 * time.Second,
						MaxBiStreamsConcurrent:     100,
						MaxUniStreamsConcurrent:    100,
						MaxStreamReceiveWindow:     6 * 1024 * 1024,
						MaxConnectionReceiveWindow: 15 * 1024 * 1024,
					},
				},
				LLM: baseLLMConfig,
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			expectError: true,
			errorMsg:    "HTTP/3 TLS certificate file not accessible",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDefaultHTTP3Config(t *testing.T) {
	cfg := DefaultConfig()
	require.NotNil(t, cfg.Server.HTTP3)
	assert.False(t, cfg.Server.HTTP3.Enabled)
	assert.Equal(t, 443, cfg.Server.HTTP3.Port)
	assert.Equal(t, 30*time.Second, cfg.Server.HTTP3.IdleTimeout)
	assert.Equal(t, int64(100), cfg.Server.HTTP3.MaxBiStreamsConcurrent)
	assert.Equal(t, int64(100), cfg.Server.HTTP3.MaxUniStreamsConcurrent)
	assert.Equal(t, uint64(6*1024*1024), cfg.Server.HTTP3.MaxStreamReceiveWindow)
	assert.Equal(t, uint64(15*1024*1024), cfg.Server.HTTP3.MaxConnectionReceiveWindow)
}
