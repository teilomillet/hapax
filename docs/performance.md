---
layout: page
title: Performance
nav_order: 4
---

# Performance Guide

This guide covers performance optimization strategies for Hapax, including HTTP/3, caching, queuing, and load management.

## Performance Features

### HTTP/3 Support

Hapax supports HTTP/3 (QUIC) for improved performance:

```yaml
server:
  http3:
    enabled: true
    port: 443
    tls_cert_file: "/etc/certs/server.crt"
    tls_key_file: "/etc/certs/server.key"
    idle_timeout: 30s
    max_bi_streams_concurrent: 100     # Concurrent bidirectional streams
    max_uni_streams_concurrent: 100     # Concurrent unidirectional streams
    max_stream_receive_window: 6291456       # 6MB stream window
    max_connection_receive_window: 15728640   # 15MB connection window
    enable_0rtt: true            # Enable 0-RTT for faster connections
    max_0rtt_size: 16384         # 16KB max 0-RTT size
    allow_0rtt_replay: false     # Disable replay protection
    udp_receive_buffer_size: 8388608   # 8MB UDP buffer
```

Benefits of HTTP/3:
- Improved connection establishment
- Better multiplexing
- Reduced head-of-line blocking
- Enhanced mobile performance
- Faster connection recovery

### Response Caching

Three caching strategies available:

```yaml
llm:
  cache:
    enable: true
    type: "redis"        # Options: memory, redis, file
    ttl: 24h            # Cache entry lifetime
    max_size: 1000      # Maximum entries/size
    redis:              # Redis-specific settings
      address: "localhost:6379"
      password: ${REDIS_PASSWORD}
      db: 0
```

Cache types:
- Memory: Fast, non-persistent, cleared on restart
- Redis: Persistent, distributed, good for clusters
- File: Persistent, good for single instances

### Request Queuing

Queue system for high-load scenarios:

```yaml
queue:
  enabled: true
  initial_size: 1000         # Starting queue capacity
  state_path: "/var/lib/hapax/queue.state"  # Persistence path
  save_interval: 30s         # State save frequency
```

Benefits:
- Handles traffic spikes
- Prevents system overload
- Optional state persistence
- Configurable queue size

### Circuit Breaker

Protects system from cascading failures:

```yaml
circuit_breaker:
  max_requests: 100          # Requests in half-open state
  interval: 30s              # Monitoring interval
  timeout: 10s              # Time in open state
  failure_threshold: 5      # Failures before opening
```

States:
- Closed: Normal operation
- Open: Stop requests after failures
- Half-Open: Testing recovery

### Provider Failover

Automatic provider switching for reliability:

```yaml
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

Features:
- Automatic failover
- Health monitoring
- Configurable preference order
- Seamless switching

## Performance Tuning

### Memory Optimization

Adjust these settings based on available memory:
- `max_header_bytes`: HTTP header size limit
- `max_stream_receive_window`: Per-stream buffer
- `max_connection_receive_window`: Per-connection buffer
- Cache size limits

### Concurrency Settings

Tune these for your workload:
- `max_bi_streams_concurrent`: Bidirectional streams
- `max_uni_streams_concurrent`: Unidirectional streams
- Queue size and persistence
- Circuit breaker thresholds

### Network Optimization

Network performance settings:
- HTTP/3 buffer sizes
- UDP receive buffer size
- Idle timeouts
- 0-RTT configuration

### Monitoring Performance

Use built-in metrics:
```yaml
routes:
  - path: "/metrics"
    handler: "metrics"
    version: "v1"
    methods: ["GET"]
    middleware: ["auth"]
```

Available metrics:
- Request latencies
- Queue lengths
- Cache hit rates
- Circuit breaker states
- Provider health status

## Best Practices

### Development Environment
```yaml
server:
  port: 8080
  http3:
    enabled: false
llm:
  cache:
    type: "memory"
    max_size: 1000
queue:
  enabled: false
```

### Production Environment
```yaml
server:
  port: 443
  http3:
    enabled: true
    max_bi_streams_concurrent: 200
    max_stream_receive_window: 8388608  # 8MB
llm:
  cache:
    type: "redis"
    ttl: 24h
queue:
  enabled: true
  initial_size: 5000
  state_path: "/var/lib/hapax/queue.state"
circuit_breaker:
  max_requests: 200
  failure_threshold: 10
```

### High-Load Environment
```yaml
server:
  http3:
    max_bi_streams_concurrent: 500
    max_stream_receive_window: 16777216  # 16MB
    max_connection_receive_window: 33554432  # 32MB
    udp_receive_buffer_size: 16777216  # 16MB
llm:
  cache:
    type: "redis"
    max_size: 10000
queue:
  enabled: true
  initial_size: 10000
circuit_breaker:
  max_requests: 500
  interval: 60s
```

## Troubleshooting

Common performance issues and solutions:

### High Latency
- Enable HTTP/3
- Increase stream windows
- Adjust UDP buffer size
- Check provider health

### Memory Usage
- Reduce cache size
- Lower stream limits
- Adjust queue size
- Monitor metrics

### Request Failures
- Check circuit breaker logs
- Verify provider health
- Adjust retry settings
- Enable failover

### Queue Overflow
- Increase queue size
- Enable persistence
- Adjust circuit breaker
- Scale horizontally