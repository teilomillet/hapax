---
layout: page
title: Operations
nav_order: 5
---

# Operations Guide

Hapax implements a comprehensive operational architecture that provides deep visibility and control over your LLM service. This guide explains how to effectively operate, monitor, and troubleshoot your Hapax deployment through its integrated observability systems.

## Operational Architecture

The operational system in Hapax is built around a modular architecture where each component provides specific monitoring and observability capabilities while working together as a cohesive system. At the core of this architecture is the Prometheus-based metrics system, which provides real-time visibility into service performance and health. This is complemented by structured logging through zap for detailed operational insights and a sophisticated health monitoring system that actively tracks service components.

The monitoring infrastructure integrates these components through a unified configuration system that manages all operational settings through a structured YAML format:

```yaml
logging:
  level: info                   # Logging verbosity
  format: json                  # Structured output

metrics:
  enabled: true                 # Enable metrics collection
  prometheus:
    enabled: true              # Enable Prometheus endpoint

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
```

## Core Operational Features

### Metrics System

The metrics system implements comprehensive visibility through Prometheus integration. It provides real-time tracking of system performance, health, and operational status through carefully designed metrics that cover all critical aspects of the service.

The system implements several categories of metrics:

1. **HTTP Request Metrics**
   The request tracking system provides detailed visibility into API usage patterns:
   - `hapax_http_requests_total`: Tracks request volume with labels for endpoint and status
   - `hapax_http_request_duration_seconds`: Measures request latency with histogram buckets
   - `hapax_http_active_requests`: Monitors concurrent load across endpoints

2. **Provider Health Metrics**
   Provider health monitoring ensures reliable service delivery:
   - `hapax_health_check_duration_seconds`: Tracks health check performance
   - `hapax_health_check_errors_total`: Monitors provider reliability
   - `hapax_healthy_providers`: Provides real-time provider status

3. **Performance Metrics**
   Detailed performance tracking enables proactive optimization:
   - `hapax_request_latency_seconds`: Measures provider response times
   - `hapax_deduplicated_requests_total`: Tracks request deduplication
   - `hapax_rate_limit_hits_total`: Monitors rate limiting effectiveness

The metrics implementation includes automatic initialization and cleanup through deferred functions, ensuring accurate tracking even during concurrent operations. Each metric is registered with appropriate labels to enable detailed analysis and alerting.

### Logging System

The logging system implements structured logging through zap, providing comprehensive operational visibility with minimal performance impact. The system supports multiple output formats and verbosity levels:

```yaml
logging:
  level: info     # Available: debug, info, warn, error
  format: json    # Formats: json, text
```

The logging architecture includes several sophisticated features:
- Structured JSON output enables machine parsing and analysis
- Multiple verbosity levels support different operational needs
- Request tracing through correlation IDs enables request tracking
- Automatic error context enrichment provides detailed debugging
- Graceful log rotation prevents disk space issues

The system implements automatic cleanup of old logs and careful management of logging overhead to maintain high performance in production environments.

### Health Monitoring

The health monitoring system actively tracks service health through multiple integrated mechanisms:

```yaml
health_check:
  enabled: true               # Enable health monitoring
  interval: 15s              # Check frequency
  timeout: 5s                # Check deadline
  failure_threshold: 2       # Failures before marking unhealthy
```

The health check system implements several critical monitoring features:
- Provider availability monitoring through active health checks
- Request latency tracking with configurable thresholds
- Resource utilization monitoring for system stability
- Circuit breaker state tracking for failover management

Each health check component operates independently while contributing to the overall health status:
- Provider checks verify API responsiveness
- Latency monitoring ensures performance standards
- Resource checks prevent system exhaustion
- Circuit breaker monitoring enables automatic failover

### Request Processing Monitoring

The request processing system provides detailed visibility into request handling through comprehensive monitoring:

```yaml
processing:
  request_templates:
    default: "{{.Input}}"
    chat: "{{range .Messages}}{{.Role}}: {{.Content}}\n{{end}}"
  response_formatting:
    clean_json: true
    trim_whitespace: true
    max_length: 1048576
```

The system implements sophisticated request tracking:
- Template processing monitoring ensures correct formatting
- Response validation tracks formatting success
- Size limit enforcement prevents resource exhaustion
- Performance monitoring enables optimization

## Operational Best Practices

### Monitoring Setup

The monitoring setup should be configured for comprehensive visibility:

1. **Metrics Collection**
   Implement complete metrics coverage:
   - Enable and secure the Prometheus endpoint
   - Configure authentication for /metrics
   - Set up appropriate scrape intervals
   - Implement alerting on key metrics

2. **Logging Configuration**
   Configure logging for operational visibility:
   - Use JSON format in production
   - Set appropriate log levels
   - Implement log rotation
   - Configure log aggregation

3. **Health Checks**
   Implement comprehensive health monitoring:
   - Enable provider health tracking
   - Configure check intervals
   - Set appropriate thresholds
   - Monitor health metrics

### Troubleshooting Guide

The troubleshooting process should follow a systematic approach:

1. **Provider Issues**
   Investigate provider problems through metrics:
   - Check `hapax_health_check_errors_total`
   - Verify provider connectivity
   - Review error logs
   - Monitor circuit breaker

2. **Performance Problems**
   Analyze performance through monitoring:
   - Monitor request duration metrics
   - Check concurrent requests
   - Review resource usage
   - Analyze request patterns

3. **Error Investigation**
   Systematic error analysis process:
   - Check structured error logs
   - Review error metrics
   - Track correlation IDs
   - Verify configurations

4. **System Health**
   Monitor system health indicators:
   - Check runtime metrics
   - Monitor process stats
   - Verify resources
   - Review system logs

## Production Deployment

Production deployments require comprehensive operational controls:

```yaml
server:
  port: 443
  read_timeout: 30s
  write_timeout: 45s
  max_header_bytes: 2097152  # 2MB

logging:
  level: info
  format: json

metrics:
  enabled: true
  prometheus:
    enabled: true

health_check:
  enabled: true
  interval: 15s
  timeout: 5s
  failure_threshold: 2
```

### Monitoring Integration

1. **Prometheus Setup**
   Configure Prometheus for comprehensive monitoring:
   ```yaml
   global:
     scrape_interval: 15s
     evaluation_interval: 15s

   scrape_configs:
     - job_name: 'hapax'
       static_configs:
         - targets: ['hapax:8080']
       metrics_path: '/metrics'
   ```

2. **Logging Integration**
   Implement robust logging infrastructure:
   - Configure log aggregation
   - Set up analysis tools
   - Implement retention
   - Enable audit logging

3. **Alerting Configuration**
   Set up comprehensive alerting:
   - Configure error alerts
   - Monitor provider health
   - Track performance
   - Alert on resources

### Operational Checklist

Before deploying to production:
- Configure appropriate log levels
- Enable metrics collection
- Set up health monitoring
- Configure alerting
- Implement log rotation
- Secure metrics endpoint
- Set up monitoring dashboards
- Configure backup providers
