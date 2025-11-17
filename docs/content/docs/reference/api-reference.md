+++
disableToc = false
title = "API Reference"
weight = 22
icon = "api"
description = "Complete API reference for LocalAI's OpenAI-compatible endpoints"
+++

LocalAI provides a REST API that is compatible with OpenAI's API specification. This document provides a complete reference for all available endpoints.

## Base URL

All API requests should be made to:

```
http://localhost:8080/v1
```

For production deployments, replace `localhost:8080` with your server's address.

## Authentication

If API keys are configured (via `API_KEY` environment variable), include the key in the `Authorization` header:

```bash
Authorization: Bearer your-api-key
```

## Endpoints

### Chat Completions

Create a model response for the given chat conversation.

**Endpoint**: `POST /v1/chat/completions`

**Request Body**:

```json
{
  "model": "gpt-4",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello!"}
  ],
  "temperature": 0.7,
  "max_tokens": 100,
  "top_p": 1.0,
  "top_k": 40,
  "stream": false
}
```

**Parameters**:

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `model` | string | The model to use | Required |
| `messages` | array | Array of message objects | Required |
| `temperature` | number | Sampling temperature (0-2) | 0.7 |
| `max_tokens` | integer | Maximum tokens to generate | Model default |
| `top_p` | number | Nucleus sampling parameter | 1.0 |
| `top_k` | integer | Top-k sampling parameter | 40 |
| `stream` | boolean | Stream responses | false |
| `tools` | array | Available tools/functions | - |
| `tool_choice` | string | Tool selection mode | "auto" |

**Response**:

```json
{
  "id": "chatcmpl-123",
  "object": "chat.completion",
  "created": 1677652288,
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "Hello! How can I help you today?"
    },
    "finish_reason": "stop"
  }],
  "usage": {
    "prompt_tokens": 9,
    "completion_tokens": 12,
    "total_tokens": 21
  }
}
```

**Example**:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### Completions

Create a completion for the provided prompt.

**Endpoint**: `POST /v1/completions`

**Request Body**:

```json
{
  "model": "gpt-4",
  "prompt": "The capital of France is",
  "temperature": 0.7,
  "max_tokens": 10
}
```

**Parameters**:

| Parameter | Type | Description |
|-----------|------|-------------|
| `model` | string | The model to use |
| `prompt` | string | The prompt to complete |
| `temperature` | number | Sampling temperature |
| `max_tokens` | integer | Maximum tokens to generate |
| `top_p` | number | Nucleus sampling |
| `top_k` | integer | Top-k sampling |
| `stream` | boolean | Stream responses |

**Example**:

```bash
curl http://localhost:8080/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "prompt": "The capital of France is",
    "max_tokens": 10
  }'
```

### Edits

Create an edited version of the input.

**Endpoint**: `POST /v1/edits`

**Request Body**:

```json
{
  "model": "gpt-4",
  "instruction": "Make it more formal",
  "input": "Hey, how are you?",
  "temperature": 0.7
}
```

**Example**:

```bash
curl http://localhost:8080/v1/edits \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "instruction": "Make it more formal",
    "input": "Hey, how are you?"
  }'
```

### Embeddings

Get a vector representation of input text.

**Endpoint**: `POST /v1/embeddings`

**Request Body**:

```json
{
  "model": "text-embedding-ada-002",
  "input": "The food was delicious"
}
```

**Response**:

```json
{
  "object": "list",
  "data": [{
    "object": "embedding",
    "embedding": [0.1, 0.2, 0.3, ...],
    "index": 0
  }],
  "usage": {
    "prompt_tokens": 4,
    "total_tokens": 4
  }
}
```

**Example**:

```bash
curl http://localhost:8080/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-ada-002",
    "input": "The food was delicious"
  }'
```

### Audio Transcription

Transcribe audio into the input language.

**Endpoint**: `POST /v1/audio/transcriptions`

**Request**: `multipart/form-data`

**Form Fields**:

| Field | Type | Description |
|-------|------|-------------|
| `file` | file | Audio file to transcribe |
| `model` | string | Model to use (e.g., "whisper-1") |
| `language` | string | Language code (optional) |
| `prompt` | string | Optional text prompt |
| `response_format` | string | Response format (json, text, etc.) |

**Example**:

```bash
curl http://localhost:8080/v1/audio/transcriptions \
  -H "Authorization: Bearer not-needed" \
  -F file="@audio.mp3" \
  -F model="whisper-1"
```

### Audio Speech (Text-to-Speech)

Generate audio from text.

**Endpoint**: `POST /v1/audio/speech`

**Request Body**:

```json
{
  "model": "tts-1",
  "input": "Hello, this is a test",
  "voice": "alloy",
  "response_format": "mp3"
}
```

**Parameters**:

| Parameter | Type | Description |
|-----------|------|-------------|
| `model` | string | TTS model to use |
| `input` | string | Text to convert to speech |
| `voice` | string | Voice to use (alloy, echo, fable, etc.) |
| `response_format` | string | Audio format (mp3, opus, etc.) |

**Example**:

```bash
curl http://localhost:8080/v1/audio/speech \
  -H "Content-Type: application/json" \
  -d '{
    "model": "tts-1",
    "input": "Hello, this is a test",
    "voice": "alloy"
  }' \
  --output speech.mp3
```

### Image Generation

Generate images from text prompts.

**Endpoint**: `POST /v1/images/generations`

**Request Body**:

```json
{
  "prompt": "A cute baby sea otter",
  "n": 1,
  "size": "256x256",
  "response_format": "url"
}
```

**Parameters**:

| Parameter | Type | Description |
|-----------|------|-------------|
| `prompt` | string | Text description of the image |
| `n` | integer | Number of images to generate |
| `size` | string | Image size (256x256, 512x512, etc.) |
| `response_format` | string | Response format (url, b64_json) |

**Example**:

```bash
curl http://localhost:8080/v1/images/generations \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "A cute baby sea otter",
    "size": "256x256"
  }'
```

### List Models

List all available models.

**Endpoint**: `GET /v1/models`

**Query Parameters**:

| Parameter | Type | Description |
|-----------|------|-------------|
| `filter` | string | Filter models by name |
| `excludeConfigured` | boolean | Exclude configured models |

**Response**:

```json
{
  "object": "list",
  "data": [
    {
      "id": "gpt-4",
      "object": "model"
    },
    {
      "id": "gpt-4-vision-preview",
      "object": "model"
    }
  ]
}
```

**Example**:

```bash
curl http://localhost:8080/v1/models
```

## Streaming Responses

Many endpoints support streaming. Set `"stream": true` in the request:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

Stream responses are sent as Server-Sent Events (SSE):

```
data: {"id":"chatcmpl-123","object":"chat.completion.chunk",...}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk",...}

data: [DONE]
```

## Error Handling

### Error Response Format

```json
{
  "error": {
    "message": "Error description",
    "type": "invalid_request_error",
    "code": 400
  }
}
```

### Common Error Codes

| Code | Description |
|------|-------------|
| 400 | Bad Request - Invalid parameters |
| 401 | Unauthorized - Missing or invalid API key |
| 404 | Not Found - Model or endpoint not found |
| 429 | Too Many Requests - Rate limit exceeded |
| 500 | Internal Server Error - Server error |
| 503 | Service Unavailable - Model not loaded |

### Example Error Handling

```python
import requests

try:
    response = requests.post(
        "http://localhost:8080/v1/chat/completions",
        json={"model": "gpt-4", "messages": [...]},
        timeout=30
    )
    response.raise_for_status()
    data = response.json()
except requests.exceptions.HTTPError as e:
    if e.response.status_code == 404:
        print("Model not found")
    elif e.response.status_code == 503:
        print("Model not loaded")
    else:
        print(f"Error: {e}")
```

## Rate Limiting

LocalAI doesn't enforce rate limiting by default. For production deployments, implement rate limiting at the reverse proxy or application level.

## Best Practices

1. **Use appropriate timeouts**: Set reasonable timeouts for requests
2. **Handle errors gracefully**: Implement retry logic with exponential backoff
3. **Monitor token usage**: Track `usage` fields in responses
4. **Use streaming for long responses**: Enable streaming for better user experience
5. **Cache embeddings**: Cache embedding results when possible
6. **Batch requests**: Process multiple items together when possible

## See Also

- [OpenAI API Documentation](https://platform.openai.com/docs/api-reference) - Original OpenAI API reference
- [Try It Out]({{% relref "docs/getting-started/try-it-out" %}}) - Interactive examples
- [Integration Examples]({{% relref "docs/tutorials/integration-examples" %}}) - Framework integrations
- [Troubleshooting]({{% relref "docs/troubleshooting" %}}) - API issues

