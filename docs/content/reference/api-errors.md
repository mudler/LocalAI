
+++
disableToc = false
title = "API Error Reference"
weight = 26
+++

This page documents the error responses returned by the LocalAI API. LocalAI supports multiple API formats (OpenAI, Anthropic, Open Responses), each with its own error structure.

## Error Response Formats

### OpenAI-Compatible Format

Most endpoints return errors using the OpenAI-compatible format:

```json
{
  "error": {
    "code": 400,
    "message": "A human-readable description of the error",
    "type": "invalid_request_error",
    "param": null
  }
}
```

| Field     | Type              | Description                                      |
|-----------|-------------------|--------------------------------------------------|
| `code`    | `integer\|string` | HTTP status code or error code string             |
| `message` | `string`          | Human-readable error description                  |
| `type`    | `string`          | Error category (e.g., `invalid_request_error`)    |
| `param`   | `string\|null`    | The parameter that caused the error, if applicable|

This format is used by: `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`, `/v1/images/generations`, `/v1/audio/transcriptions`, `/models`, and other OpenAI-compatible endpoints.

### Anthropic Format

The `/v1/messages` endpoint returns errors in Anthropic's format:

```json
{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "message": "A human-readable description of the error"
  }
}
```

| Field         | Type     | Description                                    |
|---------------|----------|------------------------------------------------|
| `type`        | `string` | Always `"error"` for error responses           |
| `error.type`  | `string` | `invalid_request_error` or `api_error`         |
| `error.message` | `string` | Human-readable error description             |

### Open Responses Format

The `/v1/responses` endpoint returns errors with this structure:

```json
{
  "error": {
    "type": "invalid_request",
    "message": "A human-readable description of the error",
    "code": "",
    "param": "parameter_name"
  }
}
```

| Field     | Type     | Description                                          |
|-----------|----------|------------------------------------------------------|
| `type`    | `string` | One of: `invalid_request`, `not_found`, `server_error`, `model_error`, `invalid_request_error` |
| `message` | `string` | Human-readable error description                      |
| `code`    | `string` | Optional error code                                   |
| `param`   | `string` | The parameter that caused the error, if applicable    |

## HTTP Status Codes

| Code | Meaning                  | When It Occurs                                         |
|------|--------------------------|--------------------------------------------------------|
| 400  | Bad Request              | Invalid input, missing required fields, malformed JSON |
| 401  | Unauthorized             | Missing or invalid API key                             |
| 404  | Not Found                | Model or resource does not exist                       |
| 409  | Conflict                 | Resource already exists (e.g., duplicate token)        |
| 422  | Unprocessable Entity     | Validation failed (e.g., invalid parameter range)      |
| 500  | Internal Server Error    | Backend inference failure, unexpected server errors    |

## Global Error Handling

### Authentication Errors (401)

When API keys are configured (via `LOCALAI_API_KEY` or `--api-keys`), all requests must include a valid key. Keys can be provided through:

- `Authorization: Bearer <key>` header
- `x-api-key: <key>` header
- `xi-api-key: <key>` header
- `token` cookie

**Example request without a key:**

```bash
curl http://localhost:8080/v1/models \
  -H "Content-Type: application/json"
```

**Error response:**

```json
{
  "error": {
    "code": 401,
    "message": "An authentication key is required",
    "type": "invalid_request_error"
  }
}
```

The response also includes the header `WWW-Authenticate: Bearer`.

### Request Parsing Errors (400)

All endpoints return a 400 error if the request body cannot be parsed:

```json
{
  "error": {
    "code": 400,
    "message": "failed parsing request body: <details>",
    "type": ""
  }
}
```

### Not Found (404)

Requests to undefined routes return:

```json
{
  "error": {
    "code": 404,
    "message": "Resource not found"
  }
}
```

### Opaque Errors Mode

When `LOCALAI_OPAQUE_ERRORS=true` is set, all error responses return an empty body with only the HTTP status code. This is a security hardening option that prevents information leaks.

## Per-Endpoint Error Scenarios

### Chat Completions — `POST /v1/chat/completions`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Model not found in configuration | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

```bash
# Missing model field
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"messages": [{"role": "user", "content": "hello"}]}'
```

See also: [Text Generation]({{%relref "features/text-generation" %}})

### Completions — `POST /v1/completions`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

### Embeddings — `POST /v1/embeddings`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Model not found in configuration | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

See also: [Embeddings]({{%relref "features/embeddings" %}})

### Image Generation — `POST /v1/images/generations`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Model not found in configuration | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

See also: [Image Generation]({{%relref "features/image-generation" %}})

### Image Editing (Inpainting) — `POST /v1/images/edits`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Missing `image` file            | `missing image file`                  |
| 400    | Missing `mask` file             | `missing mask file`                   |
| 500    | Storage preparation failure     | `failed to prepare storage`           |

### Audio Transcription — `POST /v1/audio/transcriptions`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Missing `file` field in form data | `Bad Request`                      |
| 400    | Model not found in configuration | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

See also: [Audio to Text]({{%relref "features/audio-to-text" %}})

### Text to Speech — `POST /v1/audio/speech`, `POST /tts`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Model not found in configuration | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

See also: [Text to Audio]({{%relref "features/text-to-audio" %}})

### ElevenLabs TTS — `POST /v1/text-to-speech/:voice-id`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Model not found in configuration | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

### ElevenLabs Sound Generation — `POST /v1/sound-generation`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Model not found in configuration | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

### Reranking — `POST /v1/rerank`, `POST /jina/v1/rerank`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 422    | `top_n` less than 1            | `top_n - should be greater than or equal to 1` |
| 500    | Backend inference failure       | `Internal Server Error`              |

See also: [Reranker]({{%relref "features/reranker" %}})

### Anthropic Messages — `POST /v1/messages`

| Status | Cause                            | Error Type              | Example Message                                |
|--------|----------------------------------|-------------------------|-------------------------------------------------|
| 400    | Missing `model` field            | `invalid_request_error` | `model is required`                             |
| 400    | Model not in configuration       | `invalid_request_error` | `model configuration not found`                 |
| 400    | Missing or invalid `max_tokens`  | `invalid_request_error` | `max_tokens is required and must be greater than 0` |
| 500    | Backend inference failure        | `api_error`             | `model inference failed: <details>`             |
| 500    | Prediction failure               | `api_error`             | `prediction failed: <details>`                  |

```bash
# Missing model field
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{"messages": [{"role": "user", "content": "hello"}], "max_tokens": 100}'
```

```json
{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "message": "model is required"
  }
}
```

### Open Responses — `POST /v1/responses`

| Status | Cause                               | Error Type              | Example Message                                        |
|--------|-------------------------------------|-------------------------|--------------------------------------------------------|
| 400    | Missing `model` field               | `invalid_request`       | `model is required`                                    |
| 400    | Model not in configuration          | `invalid_request`       | `model configuration not found`                        |
| 400    | Failed to parse input               | `invalid_request`       | `failed to parse input: <details>`                     |
| 400    | `background=true` without `store=true` | `invalid_request_error` | `background=true requires store=true`               |
| 404    | Previous response not found         | `not_found`             | `previous response not found: <id>`                    |
| 500    | Backend inference failure           | `model_error`           | `model inference failed: <details>`                    |
| 500    | Prediction failure                  | `model_error`           | `prediction failed: <details>`                         |
| 500    | Tool execution failure              | `model_error`           | `failed to execute tools: <details>`                   |
| 500    | MCP configuration error             | `server_error`          | `failed to get MCP config: <details>`                  |
| 500    | No MCP servers available            | `server_error`          | `no working MCP servers found`                         |

```bash
# Missing model field
curl http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{"input": "hello"}'
```

```json
{
  "error": {
    "type": "invalid_request",
    "message": "model is required",
    "code": "",
    "param": ""
  }
}
```

### Open Responses — `GET /v1/responses/:id`

| Status | Cause                     | Error Type              | Example Message                  |
|--------|---------------------------|-------------------------|----------------------------------|
| 400    | Missing response ID       | `invalid_request_error` | `response ID is required`        |
| 404    | Response not found        | `not_found`             | `response not found: <id>`       |

### Open Responses Events — `GET /v1/responses/:id/events`

| Status | Cause                                | Error Type              | Example Message                                         |
|--------|--------------------------------------|-------------------------|---------------------------------------------------------|
| 400    | Missing response ID                  | `invalid_request_error` | `response ID is required`                               |
| 400    | Response was not created with stream | `invalid_request_error` | `cannot stream a response that was not created with stream=true` |
| 400    | Invalid `starting_after` value       | `invalid_request_error` | `starting_after must be an integer`                     |
| 404    | Response not found                   | `not_found`             | `response not found: <id>`                              |
| 500    | Failed to retrieve events            | `server_error`          | `failed to get events: <details>`                       |

### Object Detection — `POST /v1/detection`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Model not found in configuration | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

See also: [Object Detection]({{%relref "features/object-detection" %}})

### Video Generation — `POST /v1/video/generations`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Model not found in configuration | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

### Voice Activity Detection — `POST /v1/audio/vad`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Model not found in configuration | `Bad Request`                       |
| 500    | Backend inference failure       | `Internal Server Error`              |

### Tokenize — `POST /v1/tokenize`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 400    | Invalid or malformed request body | `Bad Request`                       |
| 400    | Model not found in configuration | `Bad Request`                       |

### Models — `GET /v1/models`, `GET /models`

| Status | Cause                          | Example Message                       |
|--------|--------------------------------|---------------------------------------|
| 500    | Failed to list models          | `Internal Server Error`              |

See also: [Model Gallery]({{%relref "features/model-gallery" %}})

## Handling Errors in Client Code

### Python (OpenAI SDK)

```python
from openai import OpenAI, APIError

client = OpenAI(base_url="http://localhost:8080/v1", api_key="your-key")

try:
    response = client.chat.completions.create(
        model="my-model",
        messages=[{"role": "user", "content": "hello"}],
    )
except APIError as e:
    print(f"Status: {e.status_code}, Message: {e.message}")
```

### curl

```bash
# Check HTTP status code
response=$(curl -s -w "\n%{http_code}" http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "nonexistent", "messages": [{"role": "user", "content": "hi"}]}')

http_code=$(echo "$response" | tail -1)
body=$(echo "$response" | head -1)

if [ "$http_code" -ne 200 ]; then
  echo "Error $http_code: $body"
fi
```

## Related Configuration

| Environment Variable          | Description                                    |
|-------------------------------|------------------------------------------------|
| `LOCALAI_API_KEY`             | Comma-separated list of valid API keys         |
| `LOCALAI_OPAQUE_ERRORS`       | Set to `true` to hide error details (returns empty body with status code only) |
| `LOCALAI_SUBTLEKEY_COMPARISON`| Use constant-time key comparison for timing-attack resistance |
