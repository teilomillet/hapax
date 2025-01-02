# Hapax: AI Infrastructure

Hapax is a production-ready AI infrastructure layer that ensures uninterrupted AI operations through intelligent provider management and automatic failover. Named after the Greek word ἅπαξ (meaning "once"), it embodies our core promise: configure once, then let it seamlessly manage your AI infrastructure.

## Common AI Infrastructure Challenges

Organizations face several critical challenges in managing their AI infrastructure. Service disruptions from AI provider outages create direct revenue impacts, while engineering teams dedicate significant resources to managing multiple AI providers. Teams struggle with limited visibility into AI usage across departments, compounded by complex integration requirements spanning different AI providers.

## Core Capabilities

Hapax delivers a robust infrastructure layer through three core capabilities:

### Intelligent Provider Management
The system ensures continuous service through real-time health monitoring with configurable timeouts and check intervals. Automatic failover between providers maintains zero downtime, while a sophisticated three-state circuit breaker (closed, half-open, open) with configurable thresholds prevents cascade failures. Request deduplication using the singleflight pattern optimizes resource utilization.

### Production-Ready Architecture
The architecture prioritizes reliability through high-performance request routing and load balancing. Comprehensive error handling and request validation ensure data integrity, while structured logging with request tracing enables detailed debugging. Configurable timeout and rate limiting mechanisms protect system resources.

### Security & Monitoring
Security is foundational, implemented through API key-based authentication and comprehensive request validation and sanitization. The monitoring system provides granular usage tracking per endpoint and detailed request logging for operational visibility.

## Usage Tracking & Monitoring

Hapax provides built-in monitoring capabilities through Prometheus integration, offering comprehensive visibility into your AI infrastructure:

### Request Tracking
Monitor API usage through versioned endpoints:
```bash
# Standard endpoint structure
/v1/completions
/health          # Global system health status
/v1/health       # Versioned API health status
/metrics
```

### Prometheus Integration
The monitoring system tracks essential metrics including request counts and status by endpoint, request latencies, active request volume, error rates by provider, and circuit breaker states. Health check performance metrics and request deduplication statistics provide deep insights into system efficiency.

Each metric is designed for operational visibility:
- `hapax_http_requests_total` tracks request volume by endpoint and status
- `hapax_http_request_duration_seconds` measures request latency
- `hapax_http_active_requests` shows current load by endpoint
- `hapax_errors_total` monitors error rates by type
- `circuit_breaker_state` indicates provider health status
- `hapax_health_check_duration_seconds` validates provider responsiveness
- `hapax_deduplicated_requests_total` confirms request efficiency
- `hapax_rate_limit_hits_total` tracks rate limiting by client

### Access Management
Security is enforced through API key-based authentication, with per-endpoint rate limiting and comprehensive request validation and sanitization.

## Technical Implementation

```json
// Example: Completion Request
{
    "messages": [
        {"role": "system", "content": "You are a customer service assistant."},
        {"role": "user", "content": "I need help with my order #12345"}
    ]
}
```

When your primary provider experiences issues, Hapax:
1. Detects the failure through continuous health checks (1-minute intervals)
2. Activates the circuit breaker after 3 consecutive failures
3. Routes traffic to healthy backup providers in preference order
4. Maintains detailed metrics for operational visibility

## Deployment Options

Deploy Hapax in minutes with our production-ready container:

```bash
docker run -p 8080:8080 \
  -e OPENAI_API_KEY=your_key \
  -e ANTHROPIC_API_KEY=your_key \
  -e CONFIG_PATH=/app/config.yaml \
  teilomillet/hapax:latest
```

Default configuration is provided but can be customized via `config.yaml`:
```yaml
circuitBreaker:
  maxRequests: 100
  interval: 30s
  timeout: 10s
  failureThreshold: 5

providerPreference:
  - ollama
  - anthropic
  - openai
```

## Integration Architecture

Hapax provides comprehensive integration capabilities through multiple components:

### REST API with Versioned Endpoints
The API architecture provides dedicated endpoints for core functionalities: 
- `/v1/completions` handles AI completions, 
- `/v1/health` provides versioned API health monitoring, 
- `/health` offers global system health status. 
- `/metrics` exposes Prometheus metrics for comprehensive monitoring.

### Comprehensive Monitoring
The monitoring infrastructure integrates Prometheus metrics across all critical components, enabling detailed tracking of request latencies, circuit breaker states, provider health status, and request deduplication. This comprehensive approach ensures complete operational visibility.

### Health Checks
The health monitoring system operates with enterprise-grade configurability. Check intervals default to one minute with adjustable timeouts, while failure thresholds are tuned to prevent false positives. Health monitoring extends from individual providers to Docker container status, with granular per-provider health tracking.

### Production Safeguards
System integrity is maintained through multiple safeguards: request deduplication prevents redundant processing, automatic failover ensures continuous operation, circuit breaker patterns protect against cascade failures, and structured JSON logging with correlation IDs enables thorough debugging.

## Technical Requirements

Running Hapax requires a Docker-compatible environment with network access to AI providers. The system operates efficiently with 1GB RAM, though 4GB is recommended for production deployments. Access credentials (API keys) are required for supported providers: OpenAI, Anthropic, etc./.

## Documentation

Comprehensive documentation is available through multiple resources. The [Quick Start Guide](https://github.com/teilomillet/hapax/wiki) provides initial setup instructions, while detailed information about the API and security measures can be found in the [API Documentation](docs/api.md) and [Security Overview](docs/security.md). For operational insights, consult the [Monitoring Guide](docs/monitoring.md).

## License

Licensed under Apache 2.0. See [LICENSE](LICENSE) for details.

---

For detailed technical specifications, visit our [Technical Documentation](docs/technical.md).