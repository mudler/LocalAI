+++
title = "API Discovery & Instructions"
weight = 27
toc = true
description = "Programmatic API discovery for agents, tools, and automation"
tags = ["API", "Agents", "Instructions", "Configuration", "Advanced"]
categories = ["Features"]
+++

LocalAI exposes a set of discovery endpoints that let external agents, coding assistants, and automation tools programmatically learn what the instance can do and how to control it — without reading documentation ahead of time.

## Quick start

```bash
# 1. Discover what's available
curl http://localhost:8080/.well-known/localai.json

# 2. Browse instruction areas
curl http://localhost:8080/api/instructions

# 3. Get an API guide for a specific instruction
curl http://localhost:8080/api/instructions/config-management
```

## Well-Known Discovery Endpoint

`GET /.well-known/localai.json`

Returns the instance version, all available endpoint URLs (flat and categorized), and runtime capabilities.

**Example response (abbreviated):**

```json
{
  "version": "v2.28.0",
  "endpoints": {
    "chat_completions": "/v1/chat/completions",
    "models": "/v1/models",
    "config_metadata": "/api/models/config-metadata",
    "instructions": "/api/instructions",
    "swagger": "/swagger/index.html"
  },
  "endpoint_groups": {
    "openai_compatible": { "chat_completions": "/v1/chat/completions", "..." : "..." },
    "config_management": { "config_metadata": "/api/models/config-metadata", "..." : "..." },
    "model_management": { "..." : "..." },
    "monitoring": { "..." : "..." }
  },
  "capabilities": {
    "config_metadata": true,
    "config_patch": true,
    "vram_estimate": true,
    "mcp": true,
    "agents": false,
    "p2p": false
  }
}
```

The `capabilities` object reflects the current runtime configuration — for example, `mcp` is only `true` if MCP is enabled, and `agents` is `true` only if the agent pool is running.

## Instructions API

Instructions are curated groups of related API endpoints. Each instruction maps to one or more Swagger tags and provides a focused, LLM-readable guide.

### List all instructions

`GET /api/instructions`

```bash
curl http://localhost:8080/api/instructions
```

Returns a compact list of instruction areas:

```json
{
  "instructions": [
    {
      "name": "chat-inference",
      "description": "OpenAI-compatible chat completions, text completions, and embeddings",
      "tags": ["inference", "embeddings"],
      "url": "/api/instructions/chat-inference"
    },
    {
      "name": "config-management",
      "description": "Discover, read, and modify model configuration fields with VRAM estimation",
      "tags": ["config"],
      "url": "/api/instructions/config-management"
    }
  ],
  "hint": "Fetch GET {url} for a markdown API guide. Add ?format=json for a raw OpenAPI fragment."
}
```

**Available instructions:**

| Instruction | Description |
|-------------|-------------|
| `chat-inference` | Chat completions, text completions, embeddings (OpenAI-compatible) |
| `audio` | Text-to-speech, transcription, voice activity detection, sound generation |
| `images` | Image generation and inpainting |
| `model-management` | Browse gallery, install, delete, manage models and backends |
| `config-management` | Discover, read, and modify model config fields with VRAM estimation |
| `monitoring` | System metrics, backend status, system information |
| `mcp` | Model Context Protocol — tool-augmented chat with MCP servers |
| `agents` | Agent task and job management |
| `video` | Video generation from text prompts |

### Get an instruction guide

`GET /api/instructions/:name`

By default, returns a **markdown guide** suitable for LLMs and humans:

```bash
curl http://localhost:8080/api/instructions/config-management
```

Add `?format=json` to get a raw **OpenAPI fragment** (filtered Swagger spec with only the relevant paths and definitions):

```bash
curl http://localhost:8080/api/instructions/config-management?format=json
```

## Configuration Management APIs

These endpoints let agents discover model configuration fields, read current settings, modify them, and estimate VRAM usage.

### Config metadata

`GET /api/models/config-metadata`

Returns structured metadata for all model configuration fields, organized by section. Each field includes its YAML path, Go type, UI type, label, description, default value, validation constraints, and available options.

```bash
# All fields
curl http://localhost:8080/api/models/config-metadata

# Filter by section
curl http://localhost:8080/api/models/config-metadata?section=parameters
```

### Autocomplete values

`GET /api/models/config-metadata/autocomplete/:provider`

Returns runtime values for dynamic fields. Providers include `backends`, `models`, `models:chat`, `models:tts`, `models:transcript`, `models:vad`.

```bash
# List available backends
curl http://localhost:8080/api/models/config-metadata/autocomplete/backends

# List chat-capable models
curl http://localhost:8080/api/models/config-metadata/autocomplete/models:chat
```

### Read model config

`GET /api/models/config-json/:name`

Returns the full model configuration as JSON:

```bash
curl http://localhost:8080/api/models/config-json/my-model
```

### Update model config

`PATCH /api/models/config-json/:name`

Deep-merges a JSON patch into the existing model configuration. Only include the fields you want to change:

```bash
curl -X PATCH http://localhost:8080/api/models/config-json/my-model \
  -H "Content-Type: application/json" \
  -d '{"context_size": 16384, "gpu_layers": 40}'
```

The endpoint validates the merged config and writes it to disk as YAML.

{{% notice context="warning" %}}
Config management endpoints require **admin authentication** when API keys are configured. The discovery and instructions endpoints are unauthenticated.
{{% /notice %}}

### VRAM estimation

`POST /api/models/vram-estimate`

Estimates VRAM usage for an installed model based on its weight files, context size, and GPU layer offloading:

```bash
curl -X POST http://localhost:8080/api/models/vram-estimate \
  -H "Content-Type: application/json" \
  -d '{"model": "my-model", "context_size": 8192}'
```

```json
{
  "sizeBytes": 4368438272,
  "sizeDisplay": "4.4 GB",
  "vramBytes": 6123456789,
  "vramDisplay": "6.1 GB",
  "context_note": "Estimate used default context_size=8192. The model's trained maximum context is 131072; VRAM usage will be higher at larger context sizes.",
  "model_max_context": 131072
}
```

Optional parameters: `gpu_layers` (number of layers to offload, 0 = all), `kv_quant_bits` (KV cache quantization, 0 = fp16).

## Integration guide

A recommended workflow for agent/tool builders:

1. **Discover**: Fetch `/.well-known/localai.json` to learn available endpoints and capabilities
2. **Browse instructions**: Fetch `/api/instructions` for an overview of instruction areas
3. **Deep dive**: Fetch `/api/instructions/:name` for a markdown API guide on a specific area
4. **Explore config**: Use `/api/models/config-metadata` to understand configuration fields
5. **Interact**: Use the standard OpenAI-compatible endpoints for inference, and the config management endpoints for runtime tuning

## Swagger UI

The full interactive API documentation is available at `/swagger/index.html`. All annotated endpoints can be explored and tested directly from the browser.
