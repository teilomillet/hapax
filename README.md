# Hapax

## Large Language Model Infrastructure, Simplified

Building with Large Language Models is complex. Multiple providers, varying APIs, inconsistent performance, unpredictable costs—these challenges consume more engineering time than the actual innovation.

Hapax offers a different approach. 

What if managing LLM infrastructure was as simple as editing a configuration file? What if switching providers, adding endpoints, or implementing fallback strategies could be done with minimal effort?

Imagine a system that:
- Connects to multiple LLM providers seamlessly
- Provides automatic failover between providers
- Offers comprehensive monitoring and metrics
- Allows instant configuration updates without downtime

This is Hapax.

### Real-World Flexibility in Action

Imagine you're running a production service using OpenAI's GPT model. Suddenly, you want to:
- Add a new Anthropic Claude model endpoint
- Create a fallback strategy
- Implement detailed monitoring

With Hapax, this becomes simple:

```yaml
# Simply append to your existing configuration
providers:
  anthropic:
    type: anthropic
    models:
      claude-3.5-haiku:
        api_key: ${ANTHROPIC_API_KEY}
        endpoint: /v1/anthropic/haiku
```

No downtime. No complex redeployment. Just configuration.

## Intelligent Provider Management

Hapax goes beyond simple API routing. It creates a resilient ecosystem for your LLM interactions:

**Automatic Failover**: When one provider experiences issues, Hapax seamlessly switches to backup providers. Your service continues operating without interruption.

**Deduplication**: Prevent duplicate requests and unnecessary API calls. Hapax intelligently manages request caching and prevents redundant processing.

**Provider Health Monitoring**: Continuously track provider performance. Automatically reconnect to primary providers once they're back online, ensuring optimal resource utilization.

## Comprehensive Observability

Hapax isn't just a gateway—it's a complete monitoring and alerting system for your LLM infrastructure:
- Detailed Prometheus metrics
- Real-time performance tracking
- Comprehensive error reporting
- Intelligent alerting mechanisms

## API Versioning for Scalability

Create multiple API versions effortlessly. Each endpoint can have its own configuration, allowing granular control and smooth evolutionary paths for your services.

```yaml
routes:
  - path: /v1/completions
    handler: completion
    version: v1
  - path: /v2/completions
    handler: advanced_completion
    version: v2
```

## Getting Started

```bash
# Pull and start Hapax with default configuration
docker run -p 8080:8080 -e ANTHROPIC_API_KEY=your_api_key teilomillet/hapax:latest

# Or, to use a custom configuration with environment variables:
# 1. Extract the default configuration
docker run --rm teilomillet/hapax:latest cat /app/config.yaml > config.yaml

# 2. Create a .env file to store your environment variables
echo "ANTHROPIC_API_KEY=your_api_key" > .env

# 3. Start Hapax with your configuration and environment variables
docker run -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  --env-file .env \
  teilomillet/hapax:latest
```

## What's Next

Hapax is continuously evolving. 

## Open Source

Licensed under Apache 2.0, Hapax is open for collaboration and customization.

## Community & Support

- **Discussions**: [GitHub Discussions](https://github.com/teilomillet/hapax/discussions)
- **Documentation**: [Hapax Wiki](https://github.com/teilomillet/hapax/wiki)
- **Issues**: [GitHub Issues](https://github.com/teilomillet/hapax/issues)

## Our Vision

We believe LLM infrastructure should be simple, reliable, and adaptable. Hapax represents our commitment to making LLM integration accessible and powerful.