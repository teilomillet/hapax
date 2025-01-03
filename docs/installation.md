---
layout: page
title: Installation
nav_order: 2
---

# Installation Guide

This guide helps you choose and implement the best installation method for your needs. Each method has been thoroughly tested and validated in production environments.

## Choosing Your Installation Method

Hapax offers multiple installation methods to accommodate different use cases. Whether you're evaluating the service, developing new features, or deploying to production, there's a path designed for your needs.

### Quick Decision Guide
1. **Docker Installation** (Recommended for most users)
   - Best for: Quick testing, production deployments
   - Advantages: Isolated environment, easy updates, verified base image (~17MB)
   - Trade-offs: Less customization flexibility
   
2. **Manual Installation**
   - Best for: Development, customization
   - Advantages: Full control, easier debugging, standard Go toolchain
   - Trade-offs: More setup steps, environment management

3. **Production Setup**
   - Best for: Enterprise deployments
   - Advantages: Scalability (tested to 100+ concurrent users), built-in monitoring
   - Trade-offs: More complex configuration, resource intensive

Take a moment to consider your primary goal. This will help you choose the most appropriate installation method:
- "I want to try Hapax quickly" → Docker Quick Start (5-minute setup)
- "I need to modify the code" → Manual Installation (standard Go project)
- "I'm deploying to production" → Production Setup (enterprise-ready)

## System Requirements

Before you begin installation, ensure your environment meets the necessary requirements. We've separated these into mandatory and optional components to help you plan your deployment effectively.

### Mandatory Requirements (Why?)
- **LLM Provider Access**: Core functionality depends on LLM API
- **API Keys**: Secure provider authentication
- **512MB RAM**: Verified base memory footprint
- **100MB Disk**: Tested minimum storage requirement
- **Go 1.22+**: Latest stable release support

### Optional Requirements (Why?)
- **2+ CPU Cores**: Verified for concurrent request handling
- **2GB+ RAM**: Tested for caching and queue management
- **1GB+ Disk**: Validated for logging and metrics
- **TLS Certificates**: Production security (HTTP/3 support)
- **Docker**: Industry-standard containerization

## Installation Methods

Now that you've chosen your installation method and verified your system requirements, let's proceed with the installation. Each method includes verification steps to ensure everything is working correctly.

### 1. Docker Quick Start (5 minutes)
The Docker installation method provides the fastest path to a running system. It's preconfigured with sensible defaults and includes all necessary dependencies.

```bash
docker run -p 8080:8080 \
  -e OPENAI_API_KEY=your_key \
  -e CONFIG_PATH=/app/config.yaml \
  teilomillet/hapax:latest
```

After running this command, take a moment to verify the installation:
```bash
# Should return HTTP 200
curl http://localhost:8080/health
```

### 2. Manual Installation (15 minutes)
The manual installation gives you full control over the build process and is ideal for development work. Follow these steps carefully:

1. Clone and build:
   ```bash
   git clone https://github.com/teilomillet/hapax.git
   cd hapax
   go build -o hapax cmd/hapax/main.go
   ```

2. Configure:
   ```bash
   cp config.example.yaml config.yaml
   # Required: Provider configuration
   export OPENAI_API_KEY="your_key"
   # Optional: Logging setup
   export LOG_LEVEL="info"
   ```

3. Run:
   ```bash
   ./hapax --config config.yaml
   ```

### 3. Production Setup (30 minutes)
For production environments, we recommend this more robust setup that includes logging, monitoring, and automatic restarts:

```bash
docker run -d \
  --name hapax \
  --restart unless-stopped \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/logs:/app/logs \
  -e OPENAI_API_KEY=your_key \
  --log-driver=json-file \
  --log-opt max-size=10m \
  teilomillet/hapax:latest
```

## Verification Guide

After installation, it's crucial to verify that everything is working correctly. We provide a series of checks that progress from basic connectivity to full functionality testing.

### How to Know It's Working

1. **Health Check** (Basic Verification)
   ```bash
   curl http://localhost:8080/health
   # Expected: {"status":"ok"}
   ```

2. **Functionality Test** (Core Feature Check)
   ```bash
   curl -X POST http://localhost:8080/v1/completions \
     -H "Content-Type: application/json" \
     -d '{"messages":[{"role":"user","content":"Hello"}]}'
   # Expected: Response with generated content
   ```

3. **Performance Check** (Optional)
   ```bash
   curl http://localhost:8080/metrics
   # Expected: Prometheus metrics data
   ```

### Common Issues and Solutions

If you encounter any issues during installation or verification, here are some common problems and their solutions:

1. **API Key Issues**
   - Symptom: 401 Unauthorized
   - Solution: Check environment variables
   ```bash
   echo $OPENAI_API_KEY # Should show your key
   ```

2. **Port Conflicts**
   - Symptom: Address already in use
   - Solution: Change port in config or check running processes
   ```bash
   lsof -i :8080 # Check port usage
   ```

3. **Configuration Errors**
   - Symptom: Server won't start
   - Solution: Validate configuration
   ```bash
   ./hapax --validate --config config.yaml
   ```

## When Can You Use It?

You'll know your Hapax installation is ready for use when you've completed these key checkpoints:
1. Health check returns `{"status":"ok"}`
2. Test completion request succeeds
3. No errors in logs (`docker logs hapax` or local logs)

### Next Steps After Installation
Once your installation is verified, consider these steps to enhance your deployment:
- Configure additional providers for redundancy
- Enable optional features based on your needs
- Set up monitoring for production visibility
- Implement security measures for your environment

Need help? Our documentation and community resources are here to assist:
- [Configuration Guide](configuration.md)
- [GitHub Issues](https://github.com/teilomillet/hapax/issues)
- [Full Documentation](https://teilomillet.github.io/hapax) 