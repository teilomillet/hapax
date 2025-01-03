---
layout: page
title: Security
nav_order: 4
---

# Security Guide

Hapax implements a comprehensive security architecture that protects your LLM service through multiple integrated layers. This guide explains how each security component works and how to configure them for your specific needs.

## Security Architecture

The security system in Hapax is built around a modular architecture where each component provides specific protections while working together as a cohesive system. At the core of this architecture is a configuration system that manages all security settings through a structured YAML format.

The server component forms the first line of defense, handling network security, transport encryption, and request validation. It implements timeouts, connection limits, and protocol-specific protections. Behind this, the queue system manages resource allocation and prevents system exhaustion by controlling request flow.

For external communications, the provider access system securely manages API keys and implements automatic failover mechanisms. This is complemented by a comprehensive monitoring system that provides real-time visibility into security events through structured logging and metrics collection.

Here's how these components are organized in the configuration:

```yaml
server:
  # Network and transport security settings
  read_timeout: 30s
  write_timeout: 45s
  max_header_bytes: 2097152
  http3:
    enabled: false
    # TLS and protocol security settings

queue:
  # Resource protection settings
  enabled: true
  initial_size: 1000
  # State management settings

llm:
  # Provider security settings
  api_key: ${LLM_API_KEY}
  # Backup provider configuration
  
logging:
  # Audit and monitoring settings
  level: info
  format: json

metrics:
  # Security metrics collection
  enabled: true
  # Metric configuration
```

## Core Security Features

Hapax provides security by default through carefully chosen configurations that protect your service from the moment it starts. These defaults are designed to prevent common security issues while remaining flexible enough to adapt to different deployment scenarios.

The default configuration implements several key protections:

Request timeouts prevent resource exhaustion by limiting how long a request can take. The read timeout (30 seconds) controls how long the server will wait for a complete request, while the write timeout (45 seconds) ensures responses don't hang indefinitely.

Header size limits (2MB by default) protect against memory exhaustion attacks by restricting the amount of data that can be sent in HTTP headers. This is particularly important for preventing denial of service attempts through oversized headers.

API key management is handled securely through environment variables, preventing accidental exposure in configuration files or logs. The system supports multiple provider keys with automatic failover capabilities.

Error handling includes automatic retries for specific types of failures, with configurable delays and backoff strategies. This helps maintain service stability during transient issues while preventing excessive retry attempts that could worsen an outage.

### When to Enhance Security

Your security needs will evolve as your deployment grows and faces different challenges. Consider implementing additional security measures in these scenarios:

Production Deployment: When moving from development to production, you'll need to enable additional security features like TLS certificates, rate limiting, and audit logging.

Sensitive Data Handling: If your service processes sensitive information, implement encryption at rest and in transit, along with strict access controls.

Audit Requirements: When compliance or internal policies require tracking of system access and changes, enable comprehensive audit logging and monitoring.

High-Scale Operations: As your service scales, implement additional protections against resource exhaustion and denial of service attacks.

Compliance Requirements: When meeting specific compliance standards, enable relevant security controls and monitoring capabilities.

## Core Security Components

### Request Queue Protection

The queue system provides robust protection against resource exhaustion and ensures system stability through a sophisticated request management system. Here's how it works:

```yaml
queue:
  enabled: true                # Enable queue protection
  initial_size: 1000          # Starting queue capacity
  state_path: "/var/queue"    # State persistence location
  save_interval: "30s"        # State backup frequency
```

The queue implements several critical security mechanisms:

**Request Lifecycle Management**
The request lifecycle is managed through a sophisticated FIFO queue system. When a request arrives, it is assigned a dedicated channel that signals its completion status, ensuring proper cleanup even in case of failures. The queue position is stored in the request context using a type-safe key (queuePositionKey), enabling accurate tracking and monitoring throughout the request's lifetime. The system implements automatic cleanup through multiple mechanisms: channel-based signaling for completion, defer statements for resource cleanup even during panics, and automatic queue management that removes completed requests.

**Thread Safety Protection**
Thread safety is achieved through a multi-layered approach. A read-write mutex (RWMutex) protects all queue operations, preventing race conditions during concurrent access. The queue size is managed through atomic operations, ensuring accurate counting even under high concurrency. Request completion is coordinated through channel-based synchronization, which provides a safe way to signal when requests are finished. Memory safety is further enhanced through defer-based cleanup mechanisms that ensure resources are always released properly.

**State Persistence Security**
The queue's state persistence system ensures reliable operation across server restarts through a robust atomic file operation mechanism. When saving state, the system first writes to a temporary file with carefully chosen permissions (0644) and then uses an atomic rename operation to replace the old state file, preventing corruption during saves. The storage directory is created with secure permissions (0755) if it doesn't exist. The system implements automatic recovery on startup by attempting to restore the previous state, falling back gracefully to initial configuration if the state file is missing or invalid.

**Health Monitoring**
The queue implements comprehensive health monitoring through Prometheus metrics. It actively tracks the number of requests in various states: those waiting in the queue and those being processed. The system measures queue wait times and processing duration for performance analysis. Error conditions, such as queue capacity limits and persistence failures, are tracked through dedicated error metrics. All metrics are automatically updated through deferred functions and atomic operations to ensure accuracy even during concurrent operations.

The queue system's resilience is built into every operation:
The system automatically cleans up completed requests through a combination of defer statements and channel-based signaling. When a request finishes or encounters an error, all associated resources are properly released. Memory leaks are prevented through careful resource management and Go's garbage collection. During shutdown, the system performs a graceful cleanup process, waiting for in-flight requests to complete and ensuring a final state save before terminating.

### API Key Management

The API key management system in Hapax provides secure handling of provider credentials through a flexible configuration system. The LLMConfig structure supports multiple providers (OpenAI, Anthropic, Ollama) with individual API keys and settings. Each key is stored using environment variable substitution (e.g., ${OPENAI_API_KEY}), ensuring sensitive credentials are never hardcoded in configuration files.

The system supports a primary provider configuration with backup providers for failover scenarios. Each provider configuration includes the API key, model selection, and endpoint URL, allowing for fine-grained control over service access. For example:

```yaml
llm:
  # Primary provider configuration
  provider: "anthropic"
  api_key: ${ANTHROPIC_API_KEY}
  endpoint: "https://api.anthropic.com/v1"
  
  # Backup provider configuration
  backup_providers:
    - provider: "openai"
      api_key: ${OPENAI_API_KEY}
      model: "gpt-3.5-turbo"
    - provider: "ollama"
      api_key: ${OLLAMA_API_KEY}
      model: "llama2"
```

Provider health is actively monitored through a dedicated health check system. The ProviderHealthCheck configuration enables automatic monitoring with configurable intervals and failure thresholds:

```yaml
health_check:
  enabled: true              # Enables continuous monitoring
  interval: 15s              # Health check frequency
  timeout: 5s                # Maximum time for health check
  failure_threshold: 2       # Failures before marking unhealthy
```

The system implements sophisticated error handling through a RetryConfig that manages transient failures and rate limiting. The retry mechanism uses exponential backoff with configurable parameters:

```yaml
retry:
  max_retries: 5             # Maximum retry attempts
  initial_delay: 100ms       # Starting delay
  max_delay: 5s              # Maximum backoff time
  multiplier: 1.5            # Exponential increase factor
  retryable_errors:          # Specific error handling
    - rate_limit
    - timeout
    - server_error
```

This configuration enables automatic failover between providers based on their health status and availability. The system monitors each provider's performance and automatically switches to backup providers when necessary, ensuring continuous service availability while maintaining secure API key handling.

### HTTP/3 Security

HTTP/3 support in Hapax is implemented through a comprehensive configuration system that requires mandatory TLS for enhanced security. The HTTP3Config structure defines all security-related settings and enforces secure defaults. Here's the detailed configuration:

```yaml
server:
  http3:
    enabled: true                        # HTTP/3 must be explicitly enabled
    port: 443                           # Standard HTTPS/QUIC port
    tls_cert_file: "/certs/server.crt"  # Required TLS certificate
    tls_key_file: "/certs/server.key"   # Required private key
    
    # Connection Management
    idle_timeout: 30s                   # Maximum idle connection time
    max_bi_streams_concurrent: 100      # Bidirectional stream limit
    max_uni_streams_concurrent: 100     # Unidirectional stream limit
    
    # Flow Control
    max_stream_receive_window: "6MB"    # Per-stream buffer limit
    max_connection_receive_window: "15MB" # Per-connection buffer limit
    udp_receive_buffer_size: "8MB"      # UDP socket buffer size
    
    # Early Data Security
    enable_0rtt: true                   # Optional 0-RTT support
    max_0rtt_size: "16KB"              # 0-RTT data size limit
    allow_0rtt_replay: false           # Replay protection enabled
```

The transport security system enforces mandatory TLS 1.3 for all HTTP/3 connections. This requirement ensures perfect forward secrecy and secure key exchange. The system requires valid TLS certificates and private keys, which must be specified through the TLSCertFile and TLSKeyFile configuration options.

Request protection is implemented through a sophisticated early data (0-RTT) system. While 0-RTT support can be enabled for improved performance, the system includes built-in replay protection through the allow_0rtt_replay setting. Early data is limited to 16KB by default to prevent abuse, and all requests are validated for proper headers and content.

Resource control is managed through a comprehensive flow control system. The configuration allows fine-tuning of various buffer sizes: per-stream receive windows are limited to 6MB, connection-level windows to 15MB, and UDP socket buffers to 8MB. These limits prevent memory exhaustion while maintaining good performance. Additionally, concurrent stream limits (100 for both bidirectional and unidirectional) prevent resource exhaustion from excessive stream creation.

The configuration system includes several safety measures. Certificate paths must be valid and accessible, port numbers are validated, and all security-related settings are checked during startup. The system enforces secure defaults, with HTTP/3 disabled by default and replay protection enabled when 0-RTT is used.

### Request Processing

The request processing system implements security through a carefully designed middleware chain that handles different aspects of request security. Each middleware component provides specific security features while working together to create a comprehensive protection system:

```yaml
server:
  middleware:
    - request_timer     # Performance monitoring
    - panic_recovery    # Error protection
    - cors             # Cross-origin security
    - queue            # Resource management
    - rate_limit       # Request throttling
    - auth             # Access control
    - logging          # Audit trail
```

The RequestTimer middleware provides essential performance monitoring and tracking. It wraps each HTTP handler to measure request processing time accurately. The middleware records the start time when a request arrives, tracks its execution through the handler chain, and calculates the total duration upon completion. This timing information is added to the response headers through X-Response-Time, enabling precise monitoring of request handling performance.

The PanicRecovery middleware ensures system stability by implementing a robust error recovery mechanism. It uses Go's defer mechanism to catch any panics that occur during request processing. When a panic is detected, the middleware prevents the error from crashing the server by catching it and returning a controlled 500 Internal Server Error response. This ensures that individual request failures don't affect system stability.

The CORS (Cross-Origin Resource Sharing) middleware implements a comprehensive security policy for cross-origin requests. It carefully controls which origins can access the API by setting appropriate security headers:
```yaml
cors:
  allow_origins: ["*"]                  # Origin control
  allow_methods: ["GET", "POST", "PUT", "DELETE", "OPTIONS"]  # Method restrictions
  allow_headers: ["Accept", "Authorization", "Content-Type", "X-CSRF-Token"]  # Header validation
```
The middleware handles preflight requests with proper OPTIONS response handling and enforces security policies through carefully configured headers. This ensures that the API is protected from unauthorized cross-origin access while remaining accessible to legitimate clients.

The request processing system is designed to be both secure and efficient. Each middleware component focuses on a specific security aspect while maintaining high performance through careful implementation. The system uses efficient data structures and minimizes memory allocations, ensuring that security features don't significantly impact request processing speed.

### Network Security

The network security system implements comprehensive protection through carefully configured server settings and timeout mechanisms. The ServerConfig structure defines essential security parameters that protect against various network-based attacks:

```yaml
server:
  # Basic Server Security
  port: 8080                    # Configurable server port
  read_timeout: 30s             # Request read protection
  write_timeout: 30s            # Response write protection
  max_header_bytes: 1048576     # Header size control (1MB)
  shutdown_timeout: 30s         # Graceful shutdown period

  # Health Monitoring
  health_check:
    enabled: true               # Active monitoring
    interval: 60s               # Check frequency
    timeout: 5s                 # Check deadline
    threshold: 3                # Failure limit
    checks:
      memory: "system"          # Resource monitoring
      latency: "http"          # Performance tracking
```

The server implements multiple timeout mechanisms to prevent resource exhaustion and denial of service attacks. The ReadTimeout setting limits the time allowed for reading the entire request, including the body, protecting against slow-read attacks. The WriteTimeout setting controls response writing time, preventing slow-write attacks and ensuring timely response delivery.

Resource control is implemented through careful limits on request components. The MaxHeaderBytes setting caps header size at 1MB by default, preventing header-based memory exhaustion attacks. The system also implements automatic cleanup of idle connections and proper connection state management through the shutdown timeout mechanism.

Health monitoring provides continuous security validation through active checks. The health check system monitors various aspects of server operation:
```yaml
health_check:
  memory_threshold: "90%"       # Memory usage limit
  cpu_threshold: "80%"          # CPU usage limit
  disk_threshold: "95%"         # Storage limit
  latency_threshold: "500ms"    # Response time limit
```
When health checks detect issues, the system can take automatic action to protect itself, such as temporarily rejecting new connections or initiating graceful shutdown procedures.

The network security system is designed for robust operation in production environments. It includes proper error handling for network issues, automatic recovery from transient failures, and detailed logging of security-relevant events. The system maintains high availability while protecting against common network-based attacks through its comprehensive security configuration.

### Monitoring and Auditing

The monitoring and auditing system implements comprehensive observability through structured logging, health checks, and metrics collection. The configuration system provides detailed control over monitoring behavior:

```yaml
logging:
  level: info                   # Logging verbosity
  format: json                  # Structured output

routes:
  - path: /metrics
    handler: metrics
    version: v1
    methods: [GET]
    middleware: [auth]          # Protected metrics endpoint

  - path: /health
    handler: health
    version: v1
    methods: [GET]             # Health check endpoint

  - path: /v1/completions
    handler: completion
    health_check:
      enabled: true
      interval: 30s            # Check frequency
      timeout: 5s              # Check deadline
      threshold: 3             # Failure limit
      checks:
        api: http              # API availability
        latency: threshold     # Performance monitoring
```

The logging system provides detailed security event tracking through structured JSON output. This format ensures that security-relevant events are easily parseable and can be integrated with security information and event management (SIEM) systems. The logging level can be adjusted to capture different levels of detail, from basic security events to detailed debug information.

Health monitoring is implemented through a sophisticated check system that monitors various aspects of the service:

```yaml
health_check:
  enabled: true
  interval: 15s               # Check frequency
  timeout: 5s                 # Check deadline
  failure_threshold: 2        # Failure limit
```

The health check system actively monitors API endpoints, verifies latency thresholds, and tracks resource usage. When issues are detected, the system can automatically take corrective action or alert operators. Each route can have its own health check configuration, allowing for fine-grained monitoring of different service components.

Metrics collection is implemented through a protected /metrics endpoint that provides detailed performance and security metrics:
```yaml
metrics:
  - request_duration_seconds    # Latency tracking
  - request_size_bytes         # Request size monitoring
  - response_size_bytes        # Response size tracking
  - active_requests            # Concurrent request count
  - error_total{type}          # Error tracking by type
  - circuit_breaker_state      # Circuit breaker status
```

The metrics system provides essential data for security monitoring, performance tracking, and capacity planning. All metrics are collected with appropriate labels to enable detailed analysis and alerting. The metrics endpoint is protected by authentication middleware to prevent unauthorized access to sensitive operational data.

The monitoring and auditing system is designed for production environments, providing comprehensive visibility into the service's security posture while maintaining high performance. The system includes automatic cleanup of old logs, proper metric type selection for efficiency, and careful management of monitoring overhead.

### Production Security Checklist

Before deploying Hapax to production, ensure all security features are properly configured and enabled. The following configuration represents a secure production setup:

```yaml
server:
  # Server Security
  read_timeout: 30s
  write_timeout: 45s
  max_header_bytes: 2097152      # 2MB limit
  shutdown_timeout: 30s

  # HTTP/3 Security
  http3:
    enabled: true
    port: 443
    tls_cert_file: "/path/to/cert"
    tls_key_file: "/path/to/key"
    allow_0rtt_replay: false

# Provider Security
llm:
  provider: "ollama"
  api_key: "${OLLAMA_API_KEY}"   # Environment variable
  health_check:
    enabled: true
    interval: 15s
    timeout: 5s
    failure_threshold: 2
  
  # Backup Providers
  backup_providers:
    - provider: "anthropic"
      model: "claude-3-haiku"
      api_key: "${ANTHROPIC_API_KEY}"
    - provider: "openai"
      model: "gpt-3.5-turbo"
      api_key: "${OPENAI_API_KEY}"

# Circuit Breaker Protection
circuit_breaker:
  max_requests: 100
  interval: 30s
  timeout: 10s
  failure_threshold: 5

# Request Queue Management
queue:
  enabled: true
  initial_size: 1000
  state_path: "/var/queue"
  save_interval: 30s

# Monitoring Configuration
logging:
  level: "info"
  format: "json"

# Route Security
routes:
  - path: "/v1/completions"
    middleware: ["auth", "rate-limit", "cors", "logging"]
    health_check:
      enabled: true
      interval: 30s
      timeout: 5s
      threshold: 3
```

The configuration system implements several critical security features:

Environment Variable Protection: The system includes a sophisticated environment variable expansion mechanism that supports secure variable resolution:
```go
expandEnvVars(s string) (string, error) {
    // Secure environment variable expansion with:
    // 1. Default value handling: ${VAR:-default}
    // 2. Nested variable resolution
    // 3. Syntax validation
    // 4. Logging for traceability
}
```

Default Security Configuration: The DefaultConfig() function provides secure defaults for all critical settings:
- Timeouts configured to prevent resource exhaustion
- HTTP/3 disabled by default for explicit opt-in
- Replay protection enabled for 0-RTT when HTTP/3 is used
- Authentication required for sensitive endpoints
- Health monitoring enabled with reasonable thresholds
- Circuit breaker configured for fail-fast behavior
- Queue system configured for controlled request handling

The production deployment should verify these additional security measures:
- TLS certificates are valid and properly configured
- API keys are securely stored in environment variables
- Logging is configured for security event tracking
- Health checks are enabled and properly configured
- Circuit breaker thresholds match production load
- Queue system is properly sized for expected traffic
- Monitoring endpoints are protected by authentication
- CORS settings are appropriately restrictive
- Rate limiting is configured for production traffic

### Conclusion

Hapax implements a comprehensive security architecture that protects all aspects of the service through multiple layers of defense. The security features are deeply integrated into the core functionality:

Transport Security:
The HTTP/3 implementation provides strong transport security through mandatory TLS 1.3, with careful configuration of timeouts, buffer sizes, and replay protection. The system supports both traditional HTTPS and modern QUIC protocols, ensuring secure communication across different network conditions.

Request Processing:
The middleware chain implements essential security features such as request timing, panic recovery, CORS protection, and authentication. Each component is carefully designed to handle specific security concerns while maintaining high performance through efficient implementation.

Resource Protection:
The system includes multiple resource protection mechanisms:
- Circuit breaker pattern for fail-fast behavior
- Request queue for traffic management
- Rate limiting for abuse prevention
- Memory protection through buffer limits
- Graceful shutdown for clean termination

Monitoring and Auditing:
Comprehensive observability is achieved through:
- Structured JSON logging
- Prometheus metrics collection
- Health check system
- Performance monitoring
- Security event tracking

The configuration system provides secure defaults while allowing customization for different deployment scenarios. The environment variable expansion system ensures secure handling of sensitive configuration values, and the validation system prevents misconfigurations that could impact security.

For production deployments, the system includes a detailed security checklist and configuration examples that demonstrate secure settings for all components. The documentation provides clear guidance on security best practices, configuration options, and operational considerations.

The security architecture is designed to be both comprehensive and maintainable, with clear separation of concerns and well-defined interfaces between components. This design allows for future security enhancements while maintaining compatibility with existing deployments. 