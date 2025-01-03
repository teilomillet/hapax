---
layout: page
title: Configuration
nav_order: 3
---

# Configuration Guide

Hapax provides a flexible, validated configuration system that helps you maintain a reliable and secure LLM service. This guide will help you understand and customize your deployment.

## Configuration Overview

Hapax uses a YAML-based configuration system with built-in validation and dynamic updates. The configuration is organized into logical sections:

```yaml
server:     # HTTP server settings
llm:        # LLM provider configuration
logging:    # Logging preferences
routes:     # API endpoint definitions
metrics:    # Monitoring configuration
```

## Understanding Defaults

Hapax comes with carefully chosen defaults that allow it to work out of the box for development. You only need to configure what you want to customize.

### Default Configuration

The following settings are provided by default:

```yaml
# These are built-in defaults - you don't need to copy this
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 45s
  max_header_bytes: 2097152  # 2MB
  shutdown_timeout: 30s
  http3:
    enabled: false          # Disabled by default
    port: 443              # Default HTTPS/QUIC port

llm:
  provider: "ollama"         # Default local provider
  model: "llama2"           # Default model
  max_context_tokens: 16384
  system_prompt: "You are a helpful AI assistant focused on providing accurate and detailed responses."
  options:
    temperature: 0.7        # Between 0 and 1
    top_p: 0.9             # Between 0 and 1
    frequency_penalty: 0.3  # Between -2 and 2
    presence_penalty: 0.3   # Between -2 and 2
    stream: true
  retry:
    max_retries: 5
    initial_delay: 100ms
    max_delay: 5s
    multiplier: 1.5
    retryable_errors: ["rate_limit", "timeout", "server_error"]

provider_preference:        # Order of provider selection
  - ollama
  - anthropic
  - openai

logging:
  level: "info"
  format: "json"

# Default routes
routes:
  - path: "/v1/completions"
    handler: "completion"
    version: "v1"
    methods: ["POST", "OPTIONS"]
  - path: "/health"
    handler: "health"
    version: "v1"
    methods: ["GET"]
  - path: "/metrics"
    handler: "metrics"
    version: "v1"
    methods: ["GET"]
```

### Configuration Inheritance

Configuration works in layers:
1. Built-in defaults (shown above)
2. Your `config.yaml` overrides
3. Environment variables (highest priority)

For example, this minimal `config.yaml` works because it inherits most settings:
```yaml
llm:
  provider: "anthropic"
  api_key: ${ANTHROPIC_API_KEY}
```

### When to Override Defaults

You should override defaults when:
- Switching providers (e.g., from Ollama to Anthropic)
- Running in production (ports, timeouts, etc.)
- Need different logging levels
- Custom system prompts
- Specific model requirements

The following sections detail all available options, with notes about their defaults.

## Core Components

### Server Configuration

Configure the HTTP server and its behavior:

```yaml
server:
  port: 8080                    # HTTP server port
  read_timeout: 30s             # Request read timeout
  write_timeout: 45s            # Response write timeout
  max_header_bytes: 2097152     # 2MB header limit
  shutdown_timeout: 30s         # Graceful shutdown period
  http3:                        # Optional HTTP/3 support
    enabled: false              # Enable HTTP/3 (QUIC)
    port: 443                   # HTTPS/QUIC port
    tls_cert_file: "cert.pem"   # TLS certificate path
    tls_key_file: "key.pem"     # TLS key path
    idle_timeout: 30s           # Keep-alive timeout
    max_bi_streams_concurrent: 100    # Max bidirectional streams
    max_uni_streams_concurrent: 100    # Max unidirectional streams
    max_stream_receive_window: 6291456      # 6MB stream window
    max_connection_receive_window: 15728640  # 15MB connection window
    enable_0rtt: true           # Enable 0-RTT support
    max_0rtt_size: 16384        # 16KB max 0-RTT size
    allow_0rtt_replay: false    # Disable replay protection
    udp_receive_buffer_size: 8388608  # 8MB UDP buffer
```

The server configuration supports:
- Basic HTTP server settings
- Timeouts and limits
- Graceful shutdown
- HTTP/3 (QUIC) with TLS
- Advanced performance tuning

### LLM Provider Settings
Configure your LLM providers and their preferences. Hapax supports two approaches:

#### Approach 1: Simple Configuration (Recommended)
```yaml
# Primary LLM configuration
llm:
  provider: anthropic           # Primary provider to use
  model: claude-3.5-haiku-latest
  api_key: ${ANTHROPIC_API_KEY}
  max_context_tokens: 100000
  retry:
    max_retries: 3
    initial_delay: 100ms
    max_delay: 2s
    multiplier: 2.0
    retryable_errors: ["rate_limit", "timeout", "server_error"]

# Provider definitions
providers:
  anthropic:                    # Provider name
    type: anthropic            # Provider type
    model: claude-3.5-haiku-latest
    api_key: ${ANTHROPIC_API_KEY}
  ollama:
    type: ollama
    model: llama3
    api_key: ""

# Failover configuration
provider_preference:           # Order of provider selection
  - anthropic
  - ollama
```

#### Approach 2: Legacy Configuration
```yaml
llm:
  provider: anthropic
  model: claude-3
  api_key: ${ANTHROPIC_API_KEY}
  backup_providers:            # Legacy backup provider configuration
    - provider: openai
      model: gpt-4
      api_key: ${OPENAI_API_KEY}
```

### Provider Failover
The provider failover system supports two modes:

1. **Modern Approach** (Recommended):
   - Define providers in the `providers` map
   - Set failover order in `provider_preference`
   - More flexible and supports multiple providers

2. **Legacy Approach**:
   - Use `backup_providers` in the `llm` section
   - Simple primary/backup configuration
   - Limited to one backup provider

The failover system will:
- Start with the first provider in the preference list
- If a provider fails, automatically try the next one
- Use the retry configuration to handle transient errors
- Track provider health and adjust routing accordingly

### Health Monitoring
Configure health checks to maintain service reliability:

```yaml
health_check:
  enabled: true
  interval: 15s
  timeout: 5s
```

### Logging Configuration

Configure logging behavior and output format:

```yaml
logging:
  level: "info"     # Logging level: debug, info, warn, error
  format: "json"    # Output format: json or text
```

The logging system supports:
- Multiple verbosity levels
- Structured JSON output
- Plain text format
- Environment variable configuration

## Environment Variables

Hapax supports sophisticated environment variable expansion:

```bash
# Core settings
export OPENAI_API_KEY="your-key"
export ANTHROPIC_API_KEY="your-key"
export OLLAMA_API_KEY="your-key"

# Server settings
export HAPAX_PORT="8080"
export HAPAX_HOST="0.0.0.0"

# Logging settings
export LOG_LEVEL="info"
export LOG_FORMAT="json"
```

The environment variable system supports:
- Standard variable substitution: `${VAR}`
- Default values: `${VAR:-default}`
- Nested variable references
- Validation and error handling

Example configurations:
```yaml
llm:
  provider: anthropic
  api_key: ${ANTHROPIC_API_KEY}
  endpoint: ${API_ENDPOINT:-http://localhost:11434}

server:
  port: ${PORT:-8080}
  host: ${HOST:-0.0.0.0}
```

## Advanced Features

### Dynamic Configuration
Hapax monitors your configuration file for changes and safely applies updates without restart:

```yaml
# These settings can be updated while running
llm:
  provider: "openai"
  model: "gpt-4"
  options:
    temperature: 0.7
    top_p: 0.9
```

### Health Monitoring
Configure health checks for providers and routes:

```yaml
health_check:
  enabled: true
  interval: 15s
  timeout: 5s
  failure_threshold: 2
```

### Performance Tuning
Optimize for your workload with queue and circuit breaker settings:

```yaml
queue:
  enabled: false              # Enable for high-load scenarios
  initial_size: 1000         # Default queue size
  save_interval: 30s         # State persistence interval

circuit_breaker:
  max_requests: 100          # Requests allowed in half-open state
  interval: 30s              # Monitoring interval
  timeout: 10s              # Open state duration
  failure_threshold: 5      # Failures before opening
```

## Configuration Validation

Hapax validates your configuration at startup and when changes are made. The validator checks:

#### Server Configuration
- Port number (0-65535)
- Positive timeouts (read, write, shutdown)
- Valid header size limits
- HTTP/3 settings when enabled:
  - TLS certificate and key files
  - Stream and connection limits
  - 0-RTT configuration

#### LLM Configuration
- Provider name is specified
- Model name is specified
- Valid context token limits
- API key presence

#### Logging Configuration
- Valid log levels: debug, info, warn, error
- Valid formats: json, text

#### Route Configuration
- Non-empty paths
- Valid handlers
- Version specification
- Method and middleware validation

Run manual validation with:
```bash
./hapax --validate --config config.yaml
```

## Best Practices

#### Security
- Use environment variables for sensitive data (API keys)
- Enable TLS for production deployments
- Configure appropriate timeouts
- Protect metrics endpoints with authentication

#### Reliability
- Configure multiple providers for failover
- Set up health checks for both providers and routes
- Use appropriate circuit breaker thresholds
- Implement retry logic for transient errors

#### Performance
- Enable HTTP/3 for improved performance
- Configure appropriate stream and connection limits
- Use caching for repeated requests
- Adjust queue size based on load

#### Monitoring
- Set appropriate log levels (info for production)
- Use structured JSON logging in production
- Enable metrics collection
- Configure health check intervals

#### Default Values
The system comes with production-tested defaults:
```yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 45s
  max_header_bytes: 2097152  # 2MB

llm:
  provider: "ollama"
  model: "llama2"
  max_context_tokens: 16384
  retry:
    max_retries: 5
    initial_delay: 100ms
    max_delay: 5s
    multiplier: 1.5

circuit_breaker:
  max_requests: 100
  interval: 30s
  timeout: 10s
  failure_threshold: 5

logging:
  level: "info"
  format: "json"
```

## Example Configurations

### Example Configurations

#### Basic Development Setup
Minimal configuration for local development:

```yaml
# config.yaml - Local development
llm:
  provider: "ollama"
  model: "llama2"
  max_context_tokens: 16384

logging:
  level: "debug"
  format: "text"
```

#### Cloud Provider Setup
Configuration for using cloud LLM providers:

```yaml
# config.yaml - Cloud setup
llm:
  provider: "anthropic"
  model: "claude-3-haiku"
  api_key: ${ANTHROPIC_API_KEY}
  max_context_tokens: 100000
  retry:
    max_retries: 3
    initial_delay: 100ms
    max_delay: 2s

providers:
  anthropic:
    type: anthropic
    model: claude-3-haiku
    api_key: ${ANTHROPIC_API_KEY}
  openai:
    type: openai
    model: gpt-4
    api_key: ${OPENAI_API_KEY}

provider_preference:
  - anthropic
  - openai
```

#### Production Deployment
Full production configuration with all features:

```yaml
server:
  port: 443
  read_timeout: 30s
  write_timeout: 45s
  http3:
    enabled: true
    port: 443
    tls_cert_file: "/etc/certs/server.crt"
    tls_key_file: "/etc/certs/server.key"

llm:
  provider: anthropic
  model: claude-3-haiku
  api_key: ${ANTHROPIC_API_KEY}
  max_context_tokens: 100000
  retry:
    max_retries: 3
    initial_delay: 100ms
    max_delay: 2s
    multiplier: 2.0
    retryable_errors: ["rate_limit", "timeout", "server_error"]
  cache:
    enable: true
    type: "redis"
    redis:
      address: "localhost:6379"
      password: ${REDIS_PASSWORD}
      db: 0

providers:
  anthropic:
    type: anthropic
    model: claude-3-haiku
    api_key: ${ANTHROPIC_API_KEY}
  openai:
    type: openai
    model: gpt-4
    api_key: ${OPENAI_API_KEY}

provider_preference:
  - anthropic
  - openai

health_check:
  enabled: true
  interval: 15s
  timeout: 5s
  failure_threshold: 2

circuit_breaker:
  max_requests: 100
  interval: 30s
  timeout: 10s
  failure_threshold: 5

queue:
  enabled: true
  initial_size: 1000
  state_path: "/var/lib/hapax/queue.state"
  save_interval: 30s

logging:
  level: info
  format: json
```

## Troubleshooting

### Common Configuration Issues

1. **Server Configuration**
   ```yaml
   server:
     port: -1  # Error: Invalid port (must be 0-65535)
     read_timeout: -5s  # Error: Negative timeout not allowed
   ```

2. **HTTP/3 Configuration**
   ```yaml
   server:
     http3:
       enabled: true
       # Error: Missing TLS files
       max_0rtt_size: 2097152  # Error: Exceeds 1MB limit
   ```

3. **LLM Configuration**
   ```yaml
   llm:
     # Error: Provider is required
     model: "gpt-4"
     max_context_tokens: -1  # Error: Negative tokens not allowed
   ```

4. **Logging Configuration**
   ```yaml
   logging:
     level: "invalid"  # Error: Must be debug, info, warn, or error
     format: "yaml"    # Error: Must be json or text
   ```

5. **Route Configuration**
   ```yaml
   routes:
     - path: ""        # Error: Empty path not allowed
       handler: ""     # Error: Handler is required
       version: ""     # Error: Version is required
   ```

#### Configuration Checklist

Before deploying, verify:
- All required fields are set
- Port numbers are valid (0-65535)
- Timeouts are positive values
- TLS certificates exist and are readable
- Provider API keys are available
- Log level and format are valid
- All routes have paths, handlers, and versions

#### Validation Command

Run the built-in validator:
```bash
./hapax --validate --config config.yaml
```

The validator will:
1. Check configuration syntax
2. Validate all field values
3. Verify file accessibility
4. Report specific errors
``` 