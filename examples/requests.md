# Hapax Request Examples

This document shows how to use different types of requests with Hapax.

## Simple Completion (Default)

The simplest type of request. Just provide an input text and get a completion.

```bash
# Using curl
curl -X POST http://localhost:8081/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "What is the capital of France?"
  }'
```

```json
// Response
{
    "content": "The capital of France is Paris."
}
```

## Chat Completion

For chat-style interactions with message history.

```bash
# Using curl
curl -X POST "http://localhost:8081/v1/completions?type=chat" \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "Hi, how are you?"},
      {"role": "assistant", "content": "I'm doing well, thank you! How can I help you today?"},
      {"role": "user", "content": "What's the weather like?"}
    ]
  }'
```

```json
// Response
{
    "content": "I apologize, but I don't have access to real-time weather information. To get accurate weather information, I recommend checking a weather service or website for your specific location."
}
```

## Function Calling (Future)

For structured function-like interactions.

```bash
# Using curl
curl -X POST "http://localhost:8081/v1/completions?type=function" \
  -H "Content-Type: application/json" \
  -d '{
    "function_description": "Get the weather for a specific location",
    "input": "What's the weather like in Paris?"
  }'
```

```json
// Response
{
    "content": "{\"function\": \"get_weather\", \"location\": \"Paris\", \"unit\": \"celsius\"}"
}
```

## Request Type Selection

1. **Query Parameter**: Add `?type=chat` or `?type=function` to the URL
2. **Default Behavior**: If no type is specified, the request is treated as a simple completion
3. **Request Format**: 
   - Simple completion: Just needs `input`
   - Chat: Requires `messages` array with `role` and `content`
   - Function: Needs both `input` and `function_description`

## Response Formatting

All responses are formatted according to the configuration:
- JSON responses are cleaned and properly formatted
- Whitespace is trimmed
- Responses are truncated to the configured maximum length

## Error Handling

```json
// Example error response
{
    "error": "Invalid chat request: messages array cannot be empty",
    "status": 400
}
```

Common error cases:
1. Missing required fields
2. Invalid JSON format
3. Empty messages array in chat requests
4. Request processing failures
5. LLM errors
