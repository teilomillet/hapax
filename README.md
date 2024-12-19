# Hapax

A lightweight HTTP server for Large Language Model (LLM) interactions, built with Go.

## Version
v0.0.7

## Features

- HTTP server with completion endpoint (`/v1/completions`)
- Health check endpoint (`/health`)
- Configurable server settings (port, timeouts, etc.)
- Clean shutdown handling
- Comprehensive test suite with mock LLM implementation
- Token validation with tiktoken
  - Automatic token counting
  - Context length validation
  - Max tokens validation
- Middleware architecture:
  - Request ID tracking
  - Request timing metrics
  - Panic recovery
  - CORS support
  - API key authentication
  - Rate limiting (token bucket)
- Enhanced error handling:
  - Structured JSON error responses
  - Request ID tracking in errors
  - Zap-based logging with context
  - Custom error types for different scenarios
  - Seamless error middleware integration
- Dynamic routing:
  - Version-based routing (v1, v2)
  - Route-specific middleware
  - Health check endpoints
  - Header validation
- Configurable server settings (port, timeouts, etc.)
- Comprehensive test suite with mock LLM implementation
- Token validation with tiktoken
  - Automatic token counting
  - Context length validation
  - Max tokens validation
- Middleware architecture:
  - Request ID tracking
  - Request timing metrics
  - Panic recovery
  - CORS support
- Enhanced error handling:
  - Structured JSON error responses
  - Request ID tracking in errors
  - Zap-based logging with context
  - Custom error types for different scenarios
  - Seamless error middleware integration

## Installation

```bash
go get github.com/teilomillet/hapax
```

## Configuration

Hapax uses YAML for configuration. Here's an example configuration file:

```yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 30s
  max_header_bytes: 1048576  # 1MB
  shutdown_timeout: 30s

routes:
  - path: "/completions"
    handler: "completion"
    version: "v1"
    methods: ["POST"]
    middleware: ["auth", "ratelimit"]
    headers:
      Content-Type: "application/json"
    health_check:
      enabled: true
      interval: 30s
      timeout: 5s
      threshold: 3
      checks:
        api: "http"

  - path: "/health"
    handler: "health"
    version: "v1"
    methods: ["GET"]
    health_check:
      enabled: true
      interval: 15s
      timeout: 2s
      threshold: 2
      checks:
        system: "tcp"

llm:
  provider: ollama
  model: llama2
  endpoint: http://localhost:11434
  system_prompt: "You are a helpful assistant."
  max_context_tokens: 4096  # Maximum context length for your model
  options:
    temperature: 0.7
    max_tokens: 2000

logging:
  level: info  # debug, info, warn, error
  format: json # json, text
```

### Configuration Options

#### Server Configuration
- `port`: HTTP server port (default: 8080)
- `read_timeout`: Maximum duration for reading request body (default: 30s)
- `write_timeout`: Maximum duration for writing response (default: 30s)
- `max_header_bytes`: Maximum size of request headers (default: 1MB)
- `shutdown_timeout`: Maximum duration to wait for graceful shutdown (default: 30s)

#### LLM Configuration
- `provider`: LLM provider name (e.g., "ollama", "openai")
- `model`: Model name (e.g., "llama2", "gpt-4")
- `endpoint`: API endpoint URL
- `system_prompt`: Default system prompt for conversations
- `max_context_tokens`: Maximum context length in tokens (model-dependent)
- `options`: Provider-specific options
  - `temperature`: Sampling temperature (0.0 to 1.0)
  - `max_tokens`: Maximum tokens to generate

#### Logging Configuration
- `level`: Log level (debug, info, warn, error)
- `format`: Log format (json, text)

## Quick Start

```go
package main

import (
    "context"
    "log"

    "github.com/teilomillet/hapax"
    "github.com/teilomillet/gollm"
    "go.uber.org/zap"
)

func main() {
    // Initialize logger (optional, defaults to production config)
    logger, _ := zap.NewProduction()
    defer logger.Sync()
    hapax.SetLogger(logger)

    // Create an LLM instance (using gollm)
    llm := gollm.New()

    // Create a completion handler
    handler := hapax.NewCompletionHandler(llm)

    // Create a router
    router := hapax.NewRouter(handler)

    // Use default configuration
    config := hapax.DefaultConfig()

    // Create and start server
    server := hapax.NewServer(config, router)
    if err := server.Start(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

## API Endpoints

### POST /v1/completions

Generate completions using the configured LLM.

**Request:**
```json
{
    "prompt": "Your prompt here"
}
```

**Response:**
```json
{
    "completion": "LLM generated response"
}
```

**Error Responses:**
- 400 Bad Request: Invalid JSON or missing prompt
- 405 Method Not Allowed: Wrong HTTP method
- 500 Internal Server Error: LLM error

### GET /health

Check server health status.

**Response:**
```json
{
    "status": "ok"
}
```

## Error Handling

Hapax provides structured error handling with JSON responses:

```json
{
    "type": "validation_error",
    "message": "Invalid request format",
    "request_id": "req_123abc",
    "details": {
        "field": "prompt",
        "error": "required"
    }
}
```

Error types include:
- `validation_error`: Request validation failures
- `provider_error`: LLM provider issues
- `rate_limit_error`: Rate limiting
- `internal_error`: Unexpected server errors

## Testing

The project includes a comprehensive test suite with a mock LLM implementation that can be used for testing LLM-dependent code:

```go
import "github.com/teilomillet/hapax/mock_test"

// Create a mock LLM with custom response
llm := &MockLLM{
    GenerateFunc: func(ctx context.Context, p *gollm.Prompt) (string, error) {
        return "Custom response", nil
    },
}
```

Run the tests:
```bash
go test ./...
```

## License

MIT License

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.