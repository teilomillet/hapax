---
layout: home
title: Home
nav_order: 1
---

# Hapax Documentation

{: .fs-9 }
The reliability layer between your code and LLM providers

{: .fs-6 .fw-300 }
A lightweight, production-ready infrastructure layer that ensures continuous operation through intelligent provider management and automatic failover.

[Quick Start](getting-started/5-minute-setup){: .btn .btn-primary .fs-5 .mb-4 .mb-md-0 .mr-2 }
[View Source](https://github.com/teilomillet/hapax){: .btn .fs-5 .mb-4 .mb-md-0 }

---

## Why Hapax?

{: .important }
> Hapax addresses the fundamental challenges of working with LLM providers: service reliability, provider management, and operational visibility.

### Key Benefits

{: .note }
> **Continuous Operation**  
> Automatic failover between providers maintains service availability during outages or degraded performance.

{: .note }
> **Minimal Configuration**  
> Single configuration file handles all provider settings, health checks, and failover logic.

{: .note }
> **Operational Insight**  
> Built-in metrics expose detailed provider performance, request patterns, and system health.

## Core Features

### Intelligent Provider Management
- Health monitoring with configurable thresholds
- Automatic provider failover
- Circuit breaker implementation
- Request deduplication

### System Architecture
- Request routing and load distribution
- Comprehensive error handling
- Structured logging with request tracing
- HTTP/3 support

### Security and Monitoring
- API key-based authentication
- Request validation
- Usage metrics per endpoint
- Prometheus integration

## Documentation

- [Quick Start](getting-started/5-minute-setup)
- [Core Features](core-features)
- [Production Setup](production)
- [API Reference](api)

## Development

Find issues or want to contribute?
- [Source Code](https://github.com/teilomillet/hapax)
- [Issue Tracker](https://github.com/teilomillet/hapax/issues)
- [Security Guide](production/security)
- [Configuration Reference](getting-started/configuration)