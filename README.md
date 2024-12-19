# Hapax

A lightweight HTTP server for Large Language Model (LLM) interactions, built with Go.

## Version
v0.0.4

## Features

- HTTP server with completion endpoint (`/v1/completions`)
- Health check endpoint (`/health`)
- Configurable server settings (port, timeouts, etc.)
- Clean shutdown handling
- Comprehensive test suite with mock LLM implementation
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

## Configuration

The server can be configured using the `ServerConfig` struct:

```go
type ServerConfig struct {
    Port            int           // Server port (default: 8080)
    ReadTimeout     time.Duration // HTTP read timeout (default: 30s)
    WriteTimeout    time.Duration // HTTP write timeout (default: 30s)
    MaxHeaderBytes  int          // Max header size (default: 1MB)
    ShutdownTimeout time.Duration // Graceful shutdown timeout (default: 30s)
}
```

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