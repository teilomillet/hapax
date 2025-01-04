# Hapax API Documentation

## Overview

Hapax provides a reliable API layer for interacting with LLM providers. This documentation covers the available endpoints, authentication, request/response formats, and error handling.

## Authentication

All API requests require authentication using an API key. Include your API key in the request headers:

```http
X-API-Key: your_api_key_here
```

## API Endpoints

### Completion API

The completion API supports text completion, chat completion, and function calling.

#### POST /v1/completion

Process completion requests with support for simple text input, chat messages, and function calling.

##### Request Format

```json
{
  "messages": [
    {
      "role": "user",
      "content": "Hello, how can I help you today?"
    }
  ],
  "input": "Alternative simple text input",
  "function_description": "Optional function description for function calling"
}
```

**Parameters:**

- `messages` (array, optional): Array of message objects with `role` and `content`. Required if `input` is not provided.
  - `role` (string): Message role ("user", "assistant", "system")
  - `content` (string): Message content
- `input` (string, optional): Simple text input for backward compatibility. Required if `messages` is not provided.
- `function_description` (string, optional): Description of the function for function calling requests.

##### Response Format

```json
{
  "completion": "Response text from the LLM",
  "request_id": "unique-request-id"
}
```

##### Error Responses

- `400 Bad Request`: Invalid request format or missing required fields
- `401 Unauthorized`: Invalid or missing API key
- `429 Too Many Requests`: Rate limit exceeded
- `500 Internal Server Error`: Processing or system error

##### Example

```bash
curl -X POST https://api.hapax.ai/v1/completion \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your_api_key_here" \
  -d '{
    "messages": [
      {
        "role": "user",
        "content": "What is the capital of France?"
      }
    ]
  }'
```

## Error Handling

All error responses follow a consistent format:

```json
{
  "error": {
    "type": "ValidationError",
    "message": "Detailed error message",
    "code": 400,
    "request_id": "unique-request-id",
    "details": {
      "field": "Additional error context"
    }
  }
}
```

### Error Types

- `ValidationError`: Request validation failures
- `AuthenticationError`: Authentication issues
- `RateLimitError`: Rate limit exceeded
- `ProcessingError`: LLM or processing failures
- `InternalError`: Unexpected system errors

## Rate Limiting

The API implements rate limiting based on your API key. Rate limits are configurable and can be monitored through the provided metrics.

Headers returned with rate limit information:
- `X-RateLimit-Limit`: Total requests allowed per window
- `X-RateLimit-Remaining`: Remaining requests in current window
- `X-RateLimit-Reset`: Time until the rate limit resets (in seconds)

## Best Practices

1. **Request IDs**: Include a `X-Request-ID` header for request tracking
2. **Retries**: Implement exponential backoff for rate limit errors
3. **Timeouts**: Set appropriate client timeouts (recommended: 30s)
4. **Monitoring**: Use the provided metrics endpoints for monitoring

## API Versioning

The API uses URL versioning (e.g., `/v1/completion`). Breaking changes will be introduced in new API versions, while the current version will be maintained for backward compatibility.

## Health Check API

Hapax provides comprehensive health monitoring through global, route-specific, and provider-level health check endpoints.

### GET /health

Global health check endpoint that returns the status of all services and providers.

#### Response Format

```json
{
  "status": {
    "global": true
  },
  "services": {
    "/v1/completion": "healthy",
    "/v1/other-route": "healthy"
  },
  "providers": {
    "openai": {
      "healthy": true,
      "last_check": "2024-01-01T12:00:00Z",
      "consecutive_fails": 0,
      "latency_ms": 150,
      "error_count": 0,
      "request_count": 1000
    },
    "anthropic": {
      "healthy": true,
      "last_check": "2024-01-01T12:00:00Z",
      "consecutive_fails": 0,
      "latency_ms": 200,
      "error_count": 0,
      "request_count": 500
    }
  }
}
```

- `status.global` (boolean): Overall health status
- `services` (object): Status of individual routes
  - Keys are route paths
  - Values are either "healthy" or "unhealthy"
- `providers` (object): Status of LLM providers
  - `healthy` (boolean): Whether the provider is currently operational
  - `last_check` (string): ISO 8601 timestamp of the last health check
  - `consecutive_fails` (integer): Number of consecutive health check failures
  - `latency_ms` (integer): Last observed latency in milliseconds
  - `error_count` (integer): Total number of errors since last healthy state
  - `request_count` (integer): Total number of requests processed

#### Response Codes
- `200 OK`: All services and providers are healthy
- `503 Service Unavailable`: One or more services or providers are unhealthy

### GET /health/{route}

Health check endpoint for a specific route.

#### Response Format

```json
{
  "status": "healthy"
}
```

- `status` (string): Either "healthy" or "unhealthy"

#### Response Codes
- `200 OK`: Service is healthy
- `503 Service Unavailable`: Service is unhealthy

### GET /health/provider/{provider}

Health check endpoint for a specific LLM provider.

#### Response Format

```json
{
  "healthy": true,
  "last_check": "2024-01-01T12:00:00Z",
  "consecutive_fails": 0,
  "latency_ms": 150,
  "error_count": 0,
  "request_count": 1000
}
```

#### Response Codes
- `200 OK`: Provider is healthy
- `503 Service Unavailable`: Provider is unhealthy
- `404 Not Found`: Provider not found

### Health Check Behavior

1. **Check Frequency**
   - Health checks run every minute
   - Providers are checked in parallel
   - Results are cached until next check

2. **Provider Health Checks**
   - Simple prompt sent to verify provider responsiveness
   - 5-second timeout for each check
   - Consecutive failures tracked
   - Latency monitored

3. **Health Status Transitions**
   - Provider marked unhealthy after any failure
   - Error count reset when returning to healthy state
   - Metrics updated on status changes

4. **Monitoring Integration**
   - Health status exposed via Prometheus metrics
   - `hapax_provider_healthy`: Gauge (0/1) for each provider
   - `hapax_health_check_duration_seconds`: Health check latency
   - `hapax_health_check_errors_total`: Total health check failures

## Metrics API

Hapax exposes Prometheus metrics for monitoring and observability.

### GET /metrics

Returns Prometheus-formatted metrics about the server's operation.

#### Available Metrics

1. **HTTP Request Metrics**
   - `hapax_http_requests_total`: Total number of HTTP requests by endpoint and status
   - `hapax_http_request_duration_seconds`: Duration of HTTP requests in seconds
   - `hapax_http_active_requests`: Number of currently active HTTP requests

2. **Error Metrics**
   - `hapax_errors_total`: Total number of errors by type

3. **Rate Limiting Metrics**
   - `hapax_rate_limit_hits_total`: Total number of rate limit hits by client

4. **System Metrics**
   - Standard Go runtime metrics (memory, goroutines, etc.)
   - Process metrics (CPU, file descriptors, etc.)

#### Response Format

Plain text in Prometheus exposition format:

```
# HELP hapax_http_requests_total Total number of HTTP requests by endpoint and status
# TYPE hapax_http_requests_total counter
hapax_http_requests_total{endpoint="/health",status="200"} 42
...
```

#### Response Codes
- `200 OK`: Metrics successfully retrieved

#### Example

```bash
curl http://your-server:8080/metrics
``` 

## Security

### Authentication

All API requests require authentication using an API key. The API key must be included in the request headers:

```http
X-API-Key: your_api_key_here
```

#### Obtaining API Keys
Contact your system administrator or use the Hapax management interface to generate API keys.

#### API Key Best Practices
1. **Secure Storage**: Store API keys securely and never commit them to version control
2. **Regular Rotation**: Rotate API keys periodically (recommended: every 90 days)
3. **Least Privilege**: Use different API keys for different environments (development, staging, production)
4. **Monitoring**: Monitor API key usage through the metrics endpoint
5. **Revocation**: Have a process ready to quickly revoke compromised API keys

### Security Best Practices
1. **TLS**: Always use HTTPS for API requests
2. **Request IDs**: Include unique request IDs for request tracing
3. **Timeouts**: Implement appropriate timeouts to prevent hanging connections
4. **Rate Limiting**: Respect rate limits and implement exponential backoff
5. **Error Handling**: Never expose internal errors to clients
6. **Input Validation**: Validate all input parameters before processing

## Request Flow

### Request Lifecycle
1. **Connection Establishment**
   - TLS handshake (HTTP/3 with 0-RTT support)
   - Connection multiplexing optimization

2. **Request Processing**
   - Authentication verification
   - Rate limit checking
   - Request validation
   - Request queuing (if enabled)
   - LLM provider selection
   - Response generation
   - Response formatting

3. **Response Delivery**
   - Response compression (if enabled)
   - Response streaming (for supported endpoints)

### Middleware Stack

Hapax employs several middleware components to ensure reliable and secure API operations:

1. **Authentication Middleware**
   - Validates API keys
   - Enforces authentication requirements
   - Returns `401 Unauthorized` for invalid keys

2. **Request ID Middleware**
   - Generates unique request IDs
   - Accepts client-provided IDs via `X-Request-ID`
   - Ensures request traceability

3. **Rate Limiting Middleware**
   - Enforces per-key rate limits
   - Returns rate limit headers:
     ```http
     X-RateLimit-Limit: 1000
     X-RateLimit-Remaining: 999
     X-RateLimit-Reset: 1640995200
     ```

4. **Timeout Middleware**
   - Enforces request timeouts
   - Default timeout: 30 seconds
   - Configurable per route

5. **Panic Recovery Middleware**
   - Catches unhandled panics
   - Returns `500 Internal Server Error`
   - Logs error details for debugging

6. **Metrics Middleware**
   - Records request metrics
   - Tracks response times
   - Monitors error rates

7. **Queue Middleware**
   - Manages request queuing
   - Implements fair scheduling
   - Prevents system overload

8. **CORS Middleware**
   - Handles cross-origin requests
   - Configurable CORS policies
   - Pre-flight request handling

### Request Validation

All requests are validated for:

1. **Content Type**
   - Must be `application/json` for POST requests
   - Charset must be UTF-8

2. **Request Size**
   - Maximum body size: 1MB
   - Configurable per route

3. **Required Fields**
   - All required fields must be present
   - Field types must match specifications

4. **Input Format**
   - JSON must be well-formed
   - Strings must be valid UTF-8
   - Numbers must be within allowed ranges

## Client Libraries

Official client libraries are available for:

- Python: `hapax-python`
- JavaScript/TypeScript: `@hapax/node`
- Go: `github.com/teilomillet/hapax/client`

Example using the Python client:

```python
from hapax import Client

client = Client(api_key="your_api_key_here")

response = client.completion(
    messages=[
        {"role": "user", "content": "What is the capital of France?"}
    ]
)

print(response.completion)
```

## API Status

Current API version: `v1`

### Version History
- `v1` (Current): Initial stable release
  - Basic completion API
  - Health checks
  - Metrics endpoint

### Deprecation Policy
- Major versions are supported for at least 12 months
- Deprecation notices are announced 6 months in advance
- Security patches are provided for all supported versions 