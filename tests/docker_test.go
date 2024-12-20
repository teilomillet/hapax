package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testTimeout    = 60 * time.Second
	containerName  = "hapax-test"
	containerPort  = "8080"
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
		exec.CommandContext(cleanupCtx, "docker", "rm", "-f", containerName).Run()
	}
	cleanup() // Clean up any leftover containers
	defer cleanup()

	// Create a temporary test environment
	tmpDir := t.TempDir()
	
	// Create test configuration file
	// This configuration:
	// - Sets up a basic HTTP server on port 8080
	// - Configures OpenAI as the LLM provider (required for integration tests)
	// - Enables Prometheus metrics
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := []byte(`
server:
  port: 8080
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
	require.NoError(t, os.WriteFile(configPath, configContent, 0644))

	// Create a test Dockerfile that:
	// 1. Uses multi-stage build for smaller final image
	// 2. Builds the application in a builder stage
	// 3. Creates a minimal runtime image with only necessary components
	// 4. Runs as non-root user for security
	// 5. Includes health check to verify application status
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
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
COPY config.yaml ./config.yaml
USER hapax
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -f http://localhost:8080/health || exit 1
ENTRYPOINT ["./hapax"]
CMD ["--config", "config.yaml"]
`)
	require.NoError(t, os.WriteFile(dockerfilePath, dockerfileContent, 0644))

	// Copy all required project files to test directory
	// This includes source code, dependencies, and configuration
	for _, item := range []string{"go.mod", "go.sum", "cmd", "server", "errors", "examples", "config"} {
		cmd := exec.Command("cp", "-r", filepath.Join(projectRoot, item), tmpDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		require.NoError(t, cmd.Run(), "Copy "+item+" should succeed")
	}

	// Copy test config to temp dir to ensure it's included in the build
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.yaml"), configContent, 0644))

	// Build the Docker image
	// We output build logs to help diagnose any build failures
	cmd := exec.CommandContext(ctx, "docker", "build", "-t", containerName, "-f", dockerfilePath, tmpDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "Docker build should succeed")

	// Start the container with:
	// - JSON file logging for better log parsing
	// - Port mapping to access the application
	// - Container name for easy reference
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
	// This is necessary because the application needs time to:
	// 1. Start the HTTP server
	// 2. Initialize the LLM client
	// 3. Complete the first health check
	time.Sleep(5 * time.Second)

	// Check container status to verify it's running properly
	// This helps diagnose if the container crashed or failed to start
	cmd = exec.CommandContext(ctx, "docker", "inspect", "--format={{.State.Status}} {{.State.ExitCode}}", containerName)
	status, err := cmd.Output()
	require.NoError(t, err, "Should get container status")
	t.Logf("Container status: %s", string(status))

	// Fetch container logs to help diagnose any startup issues
	// We use --details to get additional metadata with the logs
	cmd = exec.CommandContext(ctx, "docker", "logs", "--details", containerName)
	logs, err := cmd.CombinedOutput()
	if err == nil {
		t.Logf("Container logs:\n%s", string(logs))
	} else {
		t.Logf("Error getting logs: %v\n%s", err, string(logs))
	}

	// Test the health check endpoint
	t.Run("Health Check", func(t *testing.T) {
		var resp *http.Response
		var err error
		var lastErr error
		
		// Try health check with retries
		// This handles the case where the application might take longer to start
		for i := 0; i < 3; i++ {
			resp, err = http.Get(healthEndpoint)
			if err == nil && resp.StatusCode == http.StatusOK {
				break
			}
			lastErr = err
			if resp != nil {
				resp.Body.Close()
			}

			// On retry, print container status and logs to help diagnose issues
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

		// Verify health check response format
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Should read response body")

		var health struct {
			Status string `json:"status"`
		}
		require.NoError(t, json.Unmarshal(body, &health))
		assert.Equal(t, "ok", health.Status)
	})

	// Test the Prometheus metrics endpoint
	// This verifies that:
	// 1. The endpoint is accessible
	// 2. Returns correct content type for Prometheus
	// 3. Contains our application metrics
	t.Run("Metrics Endpoint", func(t *testing.T) {
		resp, err := http.Get("http://localhost:" + containerPort + "/metrics")
		require.NoError(t, err, "Metrics request should succeed")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "text/plain; version=0.0.4; charset=utf-8", resp.Header.Get("Content-Type"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Should read metrics")
		assert.Contains(t, string(body), "hapax_")
	})
}

func TestDockerCompose(t *testing.T) {
	ctx := context.Background()
	cleanup := func() {
		cmd := exec.CommandContext(ctx, "docker", "compose", "-f", filepath.Join(projectRoot, "docker-compose.yml"), "down", "-v")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}
	cleanup() // Clean up any leftover containers
	defer cleanup()

	// Create test config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := []byte(`
server:
  port: 8080
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

	// Start services with test config
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", filepath.Join(projectRoot, "docker-compose.yml"), "--env-file", "/dev/null", "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "HAPAX_CONFIG="+configPath)
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
