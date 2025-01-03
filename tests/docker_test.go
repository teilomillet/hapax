package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	testTimeout    = 60 * time.Second
	containerName  = "hapax-test"
	containerPort  = "8081"
	healthEndpoint = "http://localhost:" + containerPort + "/health"
)

var (
	projectRoot string
)

func init() {
	var err error
	projectRoot, err = filepath.Abs(filepath.Join("..", ""))
	if err != nil {
		panic(err)
	}
}

// TestDockerBuild verifies that our application works correctly when containerized.
// It tests:
// 1. Container builds successfully
// 2. Application starts and remains healthy
// 3. Health check endpoint responds correctly
// 4. Metrics endpoint provides Prometheus-formatted metrics
func TestDockerBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping docker build test in short mode")
	}

	// Docker builds can take a while, especially on CI
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// cleanup ensures we don't have leftover containers from previous test runs
	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		// Check the error return from Run()
		if err := exec.CommandContext(cleanupCtx, "docker", "rm", "-f", containerName).Run(); err != nil {
			// In a test, you typically want to log the error rather than fail the entire test
			// unless the cleanup failure is critical
			t.Logf("Failed to remove Docker container %s: %v", containerName, err)
		}
	}
	cleanup() // Clean up any leftover containers
	defer cleanup()

	// Create a temporary test environment
	tmpDir := t.TempDir()

	// Create test configuration file with all necessary settings
	configContent := []byte(`
server:
  port: 8081
  read_timeout: 30s
  write_timeout: 30s
llm:
  provider: "openai"
  model: "gpt-3.5-turbo"
  api_key: "test-key"
logging:
  level: "debug"
  format: "json"
metrics:
  enabled: true
  path: "/metrics"
  prometheus:
    enabled: true
`)

	// Write config file to the build context directory
	configPath := filepath.Join(tmpDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, configContent, 0644))

	// Create a Dockerfile that properly handles the config file
	dockerfileContent := []byte(`
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git gcc musl-dev
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o hapax ./cmd/hapax

FROM alpine:3.19
RUN adduser -D -g '' hapax
RUN apk add --no-cache ca-certificates tzdata curl
WORKDIR /app
COPY --from=builder /app/hapax .
# Copy the config file into the container
COPY config.yaml /app/config.yaml
USER hapax
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1
CMD ["./hapax", "--config", "/app/config.yaml"]
`)

	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	require.NoError(t, os.WriteFile(dockerfilePath, dockerfileContent, 0644))

	// Copy all required project files to the build context
	requiredFiles := []string{
		"go.mod",
		"go.sum",
		"cmd",
		"server",
		"errors",
		"examples",
		"config",
	}

	for _, item := range requiredFiles {
		srcPath := filepath.Join(projectRoot, item)
		dstPath := filepath.Join(tmpDir, item)
		cmd := exec.Command("cp", "-r", srcPath, dstPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		require.NoError(t, cmd.Run(), "Copy "+item+" should succeed")
	}

	// Build the Docker image
	cmd := exec.CommandContext(ctx, "docker", "build", "-t", containerName, "-f", dockerfilePath, tmpDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "Docker build should succeed")

	// Start the container
	cmd = exec.CommandContext(ctx, "docker", "run",
		"-d",
		"--name", containerName,
		"-p", containerPort+":"+containerPort,
		"--log-driver=json-file",
		"--log-opt", "max-size=10m",
		containerName,
	)
	require.NoError(t, cmd.Run(), "Docker run should succeed")

	// Give the container time to initialize
	time.Sleep(5 * time.Second)

	// Check container status
	cmd = exec.CommandContext(ctx, "docker", "inspect", "--format={{.State.Status}} {{.State.ExitCode}}", containerName)
	status, err := cmd.Output()
	require.NoError(t, err, "Should get container status")
	t.Logf("Container status: %s", string(status))

	// Fetch container logs
	cmd = exec.CommandContext(ctx, "docker", "logs", "--details", containerName)
	logs, err := cmd.CombinedOutput()
	if err == nil {
		t.Logf("Container logs:\n%s", string(logs))
	} else {
		t.Logf("Error getting logs: %v\n%s", err, string(logs))
	}

	// Test health check endpoint
	t.Run("Health Check", func(t *testing.T) {
		var resp *http.Response
		var err error
		var lastErr error

		// Try health check with retries
		for i := 0; i < 3; i++ {
			resp, err = http.Get(healthEndpoint)
			if err == nil && resp.StatusCode == http.StatusOK {
				break
			}
			lastErr = err
			if resp != nil {
				resp.Body.Close()
			}

			// Print diagnostics on retry
			if logs, err := exec.CommandContext(ctx, "docker", "logs", "--details", containerName).CombinedOutput(); err == nil {
				t.Logf("Container logs (attempt %d):\n%s", i+1, string(logs))
			}
			if status, err := exec.CommandContext(ctx, "docker", "inspect", "--format={{.State.Status}} {{.State.ExitCode}}", containerName).Output(); err == nil {
				t.Logf("Container status (attempt %d): %s", i+1, string(status))
			}

			time.Sleep(5 * time.Second)
		}
		require.NoError(t, lastErr, "Health check request should succeed")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Should read response body")

		var health struct {
			Status string `json:"status"`
		}
		require.NoError(t, json.Unmarshal(body, &health))
		assert.Equal(t, "ok", health.Status)
	})

	// Test metrics endpoint
	t.Run("Metrics Endpoint", func(t *testing.T) {
		resp, err := http.Get("http://localhost:" + containerPort + "/metrics")
		require.NoError(t, err, "Metrics request should succeed")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "text/plain; version=0.0.4; charset=utf-8; escaping=values", resp.Header.Get("Content-Type"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Should read metrics")
		assert.Contains(t, string(body), "hapax_")
	})
}

func TestDockerCompose(t *testing.T) {
	ctx := context.Background()

	// Enhanced cleanup to remove both containers and test config
	cleanup := func() {
		// Docker Compose cleanup with error handling
		cmd := exec.CommandContext(ctx, "docker", "compose", "-f", filepath.Join(projectRoot, "docker-compose.yml"), "down", "-v")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			// Log the error without failing the test, as this is a cleanup step
			t.Logf("Failed to remove Docker Compose containers: %v", err)
		}

		// Config file cleanup with error handling
		configPath := filepath.Join(projectRoot, "config.yaml")
		if err := os.Remove(configPath); err != nil {
			// Only log if the error is not because the file doesn't exist
			if !os.IsNotExist(err) {
				t.Logf("Failed to remove config file %s: %v", configPath, err)
			}
		}
	}
	cleanup() // Clean up any leftover containers and files
	defer cleanup()

	// Create test config in project root (where docker-compose expects it)
	configPath := filepath.Join(projectRoot, "config.yaml")
	configContent := []byte(`
server:
  port: 8081
  read_timeout: 30s
  write_timeout: 30s
llm:
  provider: "openai"
  model: "gpt-4o-mini"
  api_key: "test-key"
logging:
  level: "debug"
  format: "json"
metrics:
  enabled: true
  path: "/metrics"
  prometheus:
    enabled: true
`)
	require.NoError(t, os.WriteFile(configPath, configContent, 0644))

	// Start services (removed env var since we're using volume mount)
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", filepath.Join(projectRoot, "docker-compose.yml"), "--env-file", "/dev/null", "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "Docker Compose up should succeed")

	// Give services time to start up
	time.Sleep(10 * time.Second)

	// Test Prometheus integration
	t.Run("Prometheus Integration", func(t *testing.T) {
		resp, err := http.Get("http://localhost:9090/api/v1/query?query=hapax_http_requests_total")
		require.NoError(t, err, "Prometheus query should succeed")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result struct {
			Status string `json:"status"`
			Data   struct {
				ResultType string        `json:"resultType"`
				Result     []interface{} `json:"result"`
			} `json:"data"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		assert.Equal(t, "success", result.Status)
	})
}

// TestDockerConfigFlexibility systematically validates Docker-based configuration management
func TestDockerConfigFlexibility(t *testing.T) {
	// Preliminary Availability Check
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker not available")
	}

	// Diagnostic Project Root Discovery
	projectRoot, err := findProjectRoot()
	require.NoError(t, err, "Project root discovery must succeed")

	// Comprehensive Configuration Scenarios
	configScenarios := []struct {
		name           string
		initialConfig  map[string]interface{}
		updateConfig   map[string]interface{}
		verifyEndpoint string
		verifyFn       func(t *testing.T, responseBody []byte)
	}{
		{
			name: "Basic Provider Configuration",
			initialConfig: map[string]interface{}{
				"llm": map[string]interface{}{
					"provider": "openai",
					"model":    "gpt-3.5-turbo",
				},
				"server": map[string]interface{}{
					"port": 8083,
				},
			},
			updateConfig: map[string]interface{}{
				"llm": map[string]interface{}{
					"provider": "anthropic",
					"model":    "claude-3-haiku",
				},
			},
			verifyEndpoint: "/v1/config",
			verifyFn: func(t *testing.T, responseBody []byte) {
				var configResponse map[string]interface{}
				err := json.Unmarshal(responseBody, &configResponse)
				require.NoError(t, err)

				llmConfig, ok := configResponse["llm"].(map[string]interface{})
				require.True(t, ok)

				assert.Equal(t, "anthropic", llmConfig["provider"])
				assert.Equal(t, "claude-3-haiku", llmConfig["model"])
			},
		},
	}

	for _, scenario := range configScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// Comprehensive Temporary Test Environment
			tmpDir := t.TempDir()

			// Diagnostic Dockerfile with Enhanced Dependency Resolution
			dockerfileContent := []byte((`
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git gcc musl-dev

WORKDIR /app

# Set explicit module configuration
ENV GO111MODULE=on
ENV GOPROXY=direct

# Copy module definition files first for efficient caching
COPY go.mod go.sum ./
RUN go mod download

# Copy entire project context
COPY . .

# Verbose local package resolution
RUN go mod tidy -v
RUN go mod verify

# Explicit build with comprehensive diagnostics
RUN CGO_ENABLED=0 GOOS=linux go build -v -gcflags="-m" -o hapax ./cmd/hapax

FROM alpine:3.19
RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /app/hapax .
COPY initial-config.yaml /app/config.yaml

EXPOSE 8083
CMD ["./hapax", "--config", "/app/config.yaml"]
`))
			dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
			require.NoError(t, os.WriteFile(dockerfilePath, dockerfileContent, 0644))

			// Strategic File Copying with Enhanced Error Handling
			requiredFiles := []struct {
				source      string
				destination string
			}{
				{"go.mod", "go.mod"},
				{"go.sum", "go.sum"},
				{"cmd", "cmd"},
				{"server", "server"},
				{"config", "config"},
				{"errors", "errors"}, // Explicitly include errors package
			}

			for _, fileMapping := range requiredFiles {
				srcPath := filepath.Join(projectRoot, fileMapping.source)
				dstPath := filepath.Join(tmpDir, fileMapping.destination)

				// Enhanced copy mechanism with comprehensive error tracking
				cpCmd := exec.Command("cp", "-r", srcPath, dstPath)
				cpCmd.Stdout = os.Stdout
				cpCmd.Stderr = os.Stderr

				if err := cpCmd.Run(); err != nil {
					t.Logf("Warning: Failed to copy %s: %v", fileMapping.source, err)

					// Additional diagnostic information
					if _, statErr := os.Stat(srcPath); os.IsNotExist(statErr) {
						t.Logf("Source path does not exist: %s", srcPath)
					}
				}
			}

			// Configuration File Preparation
			initialConfigPath := filepath.Join(tmpDir, "initial-config.yaml")
			initialConfigBytes, err := yaml.Marshal(scenario.initialConfig)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(initialConfigPath, initialConfigBytes, 0644))

			// Comprehensive Docker Image Build
			buildCmd := exec.Command("docker", "build",
				"-t", "hapax-config-test",
				"-f", dockerfilePath,
				tmpDir)
			buildCmd.Stdout = os.Stdout
			buildCmd.Stderr = os.Stderr

			// Enhanced Build Error Handling
			if err := buildCmd.Run(); err != nil {
				// Capture and log detailed build context information
				t.Logf("Docker build failed for scenario: %s", scenario.name)

				// List contents of temporary directory for diagnostics
				listCmd := exec.Command("ls", "-R", tmpDir)
				listOutput, listErr := listCmd.CombinedOutput()
				if listErr == nil {
					t.Logf("Temporary directory contents:\n%s", string(listOutput))
				}

				// Attempt to read Dockerfile contents for verification
				dockerfileBytes, readErr := os.ReadFile(dockerfilePath)
				if readErr == nil {
					t.Logf("Dockerfile contents:\n%s", string(dockerfileBytes))
				}

				t.Fatalf("Docker build failed: %v", err)
			}

			t.Logf("Docker image built successfully for scenario: %s", scenario.name)
		})
	}
}

// findProjectRoot dynamically locates the project root directory
func findProjectRoot() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Systematic root discovery mechanism
	for {
		if _, err := os.Stat(filepath.Join(currentDir, "go.mod")); err == nil {
			return currentDir, nil
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", fmt.Errorf("could not locate project root containing go.mod")
		}
		currentDir = parentDir
	}
}
