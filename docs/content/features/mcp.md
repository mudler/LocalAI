+++
title = "🔗 Model Context Protocol (MCP)"
weight = 20
toc = true
description = "Agentic capabilities with Model Context Protocol integration"
tags = ["MCP", "Agents", "Tools", "Advanced"]
categories = ["Features"]
+++


LocalAI now supports the **Model Context Protocol (MCP)**, enabling powerful agentic capabilities by connecting AI models to external tools and services. This feature allows your LocalAI models to interact with various MCP servers, providing access to real-time data, APIs, and specialized tools.

## What is MCP?

The Model Context Protocol is a standard for connecting AI models to external tools and data sources. It enables AI agents to:

- Access real-time information from external APIs
- Execute commands and interact with external systems
- Use specialized tools for specific tasks
- Maintain context across multiple tool interactions

## Key Features

- **Real-time Tool Access**: Connect to external MCP servers for live data
- **Multiple Server Support**: Configure both remote HTTP and local stdio servers
- **Cached Connections**: Efficient tool caching for better performance
- **Secure Authentication**: Support for bearer token authentication
- **Multi-endpoint Support**: Works with OpenAI Chat, Anthropic Messages, and Open Responses APIs
- **Selective Server Activation**: Use `metadata.mcp_servers` to enable only specific servers per request
- **Server-side Tool Execution**: Tools are executed on the server and results fed back to the model automatically
- **Agent Configuration**: Customizable execution limits and retry behavior
- **MCP Prompts**: Discover and expand reusable prompt templates from MCP servers
- **MCP Resources**: Browse and inject resource content (files, data) from MCP servers into conversations

## Configuration

MCP support is configured in your model's YAML configuration file using the `mcp` section:

```yaml
name: my-mcp-model
backend: llama-cpp
parameters:
  model: qwen3-4b.gguf

mcp:
  remote: |
    {
      "mcpServers": {
        "weather-api": {
          "url": "https://api.weather.com/v1",
          "token": "your-api-token"
        },
        "search-engine": {
          "url": "https://search.example.com/mcp",
          "token": "your-search-token"
        }
      }
    }

  stdio: |
    {
      "mcpServers": {
        "file-manager": {
          "command": "python",
          "args": ["-m", "mcp_file_manager"],
          "env": {
            "API_KEY": "your-key"
          }
        },
        "database-tools": {
          "command": "node",
          "args": ["database-mcp-server.js"],
          "env": {
            "DB_URL": "postgresql://localhost/mydb"
          }
        }
      }
    }

agent:
  max_iterations: 10             # Maximum MCP tool execution loop iterations
```

### Configuration Options

#### Remote Servers (`remote`)
Configure HTTP-based MCP servers:

- **`url`**: The MCP server endpoint URL
- **`token`**: Bearer token for authentication (optional)

#### STDIO Servers (`stdio`)
Configure local command-based MCP servers:

- **`command`**: The executable command to run
- **`args`**: Array of command-line arguments
- **`env`**: Environment variables (optional)

#### Agent Configuration (`agent`)

- **`max_iterations`**: Maximum number of MCP tool execution loop iterations (default: 10). Each iteration allows the model to call tools and receive results before generating the next response.

## Usage

### Selecting MCP Servers via `metadata`

All API endpoints support MCP server selection through the standard `metadata` field. Pass a comma-separated list of server names in `metadata.mcp_servers`:

- **When present**: Only the named MCP servers are activated for this request. Server names must match the keys in the model's MCP config YAML (e.g., `"weather-api"`, `"search-engine"`).
- **When absent**: Behavior depends on the endpoint:
  - **OpenAI Chat Completions** and **Anthropic Messages**: No MCP tools are injected (standard behavior).
  - **Open Responses**: If the model has MCP config and no user-provided tools, all MCP servers are auto-activated (backward compatible).

The `mcp_servers` metadata key is consumed by the MCP engine and stripped before reaching the backend. Clients that support the standard `metadata` field can use this without custom schema extensions.

### API Endpoints

MCP tools work across all three API endpoints:

#### OpenAI Chat Completions (`/v1/chat/completions`)

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-mcp-model",
    "messages": [{"role": "user", "content": "What is the weather in New York?"}],
    "metadata": {"mcp_servers": "weather-api"},
    "stream": true
  }'
```

#### Anthropic Messages (`/v1/messages`)

```bash
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-mcp-model",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "What is the weather in New York?"}],
    "metadata": {"mcp_servers": "weather-api"}
  }'
```

#### Open Responses (`/v1/responses`)

```bash
curl http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-mcp-model",
    "input": "What is the weather in New York?",
    "metadata": {"mcp_servers": "weather-api"}
  }'
```

### Server Listing Endpoint

You can list available MCP servers and their tools for a given model:

```bash
curl http://localhost:8080/v1/mcp/servers/my-mcp-model
```

Returns:

```json
[
  {
    "name": "weather-api",
    "type": "remote",
    "tools": ["get_weather", "get_forecast"]
  },
  {
    "name": "search-engine",
    "type": "remote",
    "tools": ["web_search", "image_search"]
  }
]
```

### MCP Prompts

MCP servers can provide reusable prompt templates. LocalAI supports discovering and expanding prompts from MCP servers.

#### List Prompts

```bash
curl http://localhost:8080/v1/mcp/prompts/my-mcp-model
```

Returns:

```json
[
  {
    "name": "code-review",
    "description": "Review code for best practices",
    "title": "Code Review",
    "arguments": [
      {"name": "language", "description": "Programming language", "required": true}
    ],
    "server": "dev-tools"
  }
]
```

#### Expand a Prompt

```bash
curl -X POST http://localhost:8080/v1/mcp/prompts/my-mcp-model/code-review \
  -H "Content-Type: application/json" \
  -d '{"arguments": {"language": "go"}}'
```

Returns:

```json
{
  "messages": [
    {"role": "user", "content": "Please review the following Go code for best practices..."}
  ]
}
```

#### Inject Prompts via Metadata

You can inject MCP prompts into any chat request using `metadata.mcp_prompt` and `metadata.mcp_prompt_args`:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-mcp-model",
    "messages": [{"role": "user", "content": "Review this function: func add(a, b int) int { return a + b }"}],
    "metadata": {
      "mcp_servers": "dev-tools",
      "mcp_prompt": "code-review",
      "mcp_prompt_args": "{\"language\": \"go\"}"
    }
  }'
```

The prompt messages are prepended to the conversation before inference.

### MCP Resources

MCP servers can expose data/content (files, database records, etc.) as resources identified by URI.

#### List Resources

```bash
curl http://localhost:8080/v1/mcp/resources/my-mcp-model
```

Returns:

```json
[
  {
    "name": "project-readme",
    "uri": "file:///README.md",
    "description": "Project documentation",
    "mimeType": "text/markdown",
    "server": "file-manager"
  }
]
```

#### Read a Resource

```bash
curl -X POST http://localhost:8080/v1/mcp/resources/my-mcp-model/read \
  -H "Content-Type: application/json" \
  -d '{"uri": "file:///README.md"}'
```

Returns:

```json
{
  "uri": "file:///README.md",
  "content": "# My Project\n...",
  "mimeType": "text/markdown"
}
```

#### Inject Resources via Metadata

You can inject MCP resources into chat requests using `metadata.mcp_resources` (comma-separated URIs):

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-mcp-model",
    "messages": [{"role": "user", "content": "Summarize this project"}],
    "metadata": {
      "mcp_servers": "file-manager",
      "mcp_resources": "file:///README.md,file:///CHANGELOG.md"
    }
  }'
```

Resource contents are appended to the last user message as text blocks (following the same approach as llama.cpp's WebUI).

### Legacy Endpoint

The `/mcp/v1/chat/completions` endpoint is still supported for backward compatibility. It automatically enables all configured MCP servers (equivalent to not specifying `mcp_servers`).

```bash
curl http://localhost:8080/mcp/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-mcp-model",
    "messages": [
      {"role": "user", "content": "What is the current weather in New York?"}
    ]
  }'
```

### Example Response

```json
{
  "id": "chatcmpl-123",
  "created": 1699123456,
  "model": "my-mcp-model",
  "choices": [
    {
      "text": "The current weather in New York is 72°F (22°C) with partly cloudy skies."
    }
  ],
  "object": "text_completion"
}
```

## Example Configurations

### Docker-based Tools

```yaml
name: docker-agent
backend: llama-cpp
parameters:
  model: qwen3-4b.gguf

mcp:
  stdio: |
    {
      "mcpServers": {
        "searxng": {
          "command": "docker",
          "args": [
            "run", "-i", "--rm",
            "quay.io/mudler/tests:duckduckgo-localai"
          ]
        }
      }
    }

agent:
  max_iterations: 10
```

## How It Works

1. **Tool Discovery**: LocalAI connects to configured MCP servers and discovers available tools
2. **Tool Injection**: Discovered tools are injected into the model's tool/function list alongside any user-provided tools
3. **Inference Loop**: The model generates a response. If it calls MCP tools, LocalAI executes them server-side, appends results to the conversation, and re-runs inference
4. **Response Generation**: When the model produces a final response (no more MCP tool calls), it is returned to the client

The execution loop is bounded by `agent.max_iterations` (default 10) to prevent infinite loops.

## Session Lifecycle

MCP sessions are automatically managed by LocalAI:

- **Lazy initialization**: Sessions are created the first time a model's MCP tools are used
- **Cached per model**: Sessions are reused across requests for the same model
- **Cleanup on model unload**: When a model is unloaded (idle watchdog eviction, manual stop, or shutdown), all associated MCP sessions are closed and resources freed
- **Graceful shutdown**: All MCP sessions are closed when LocalAI shuts down

This means you don't need to manually manage MCP connections — they follow the model's lifecycle automatically.

## Supported MCP Servers

LocalAI is compatible with any MCP-compliant server.

## Best Practices

### Security
- Use environment variables for sensitive tokens
- Validate MCP server endpoints before deployment
- Implement proper authentication for remote servers

### Performance
- Cache frequently used tools
- Use appropriate timeout values for external APIs
- Monitor resource usage for stdio servers

### Error Handling
- Implement fallback mechanisms for tool failures
- Log tool execution for debugging
- Handle network timeouts gracefully

### With External Applications

Use MCP-enabled models in your applications:

```python
import openai

client = openai.OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="your-api-key"
)

response = client.chat.completions.create(
    model="my-mcp-model",
    messages=[
        {"role": "user", "content": "Analyze the latest research papers on AI"}
    ],
    extra_body={"metadata": {"mcp_servers": "search-engine"}}
)
```

### MCP and adding packages

It might be handy to install packages before starting the container to setup the environment. This is an example on how you can do that with docker-compose (installing and configuring docker)

```yaml
services:
  local-ai:
    image: localai/localai:latest
    #image: localai/localai:latest-gpu-nvidia-cuda-13
    #image: localai/localai:latest-gpu-nvidia-cuda-12
    container_name: local-ai
    restart: always
    entrypoint: [ "/bin/bash" ]
    command: >
     -c "apt-get update &&
         apt-get install -y docker.io &&
         /entrypoint.sh"
    environment:
      - DEBUG=true
      - LOCALAI_WATCHDOG_IDLE=true
      - LOCALAI_WATCHDOG_BUSY=true
      - LOCALAI_WATCHDOG_IDLE_TIMEOUT=15m
      - LOCALAI_WATCHDOG_BUSY_TIMEOUT=15m
      - LOCALAI_API_KEY=my-beautiful-api-key
      - DOCKER_HOST=tcp://docker:2376
      - DOCKER_TLS_VERIFY=1
      - DOCKER_CERT_PATH=/certs/client
    ports:
      - "8080:8080"
    volumes:
      - /data/models:/models
      - /data/backends:/backends
      - certs:/certs:ro
    # uncomment for nvidia
    # deploy:
    #   resources:
    #     reservations:
    #       devices:
    #         - capabilities: [gpu]
    #           device_ids: ['7']
    # runtime: nvidia

  docker:
    image: docker:dind
    privileged: true
    container_name: docker
    volumes:
      - certs:/certs
    healthcheck:
      test: ["CMD", "docker", "info"]
      interval: 10s
      timeout: 5s
volumes:
  certs:
```

An example model config (to append to any existing model you have) can be:

```yaml
mcp:
  stdio: |
     {
      "mcpServers": {
        "weather": {
          "command": "docker",
          "args": [
            "run", "-i", "--rm",
            "ghcr.io/mudler/mcps/weather:master"
          ]
        },
        "memory": {
          "command": "docker",
          "env": {
            "MEMORY_INDEX_PATH": "/data/memory.bleve"
          },
          "args": [
            "run", "-i", "--rm", "-v", "/host/data:/data",
            "ghcr.io/mudler/mcps/memory:master"
          ]
        },
        "ddg": {
          "command": "docker",
          "env": {
            "MAX_RESULTS": "10"
          },
          "args": [
            "run", "-i", "--rm", "-e", "MAX_RESULTS",
            "ghcr.io/mudler/mcps/duckduckgo:master"
          ]
        }
      }
     }
```

### Links

- [Awesome MCPs](https://github.com/punkpeye/awesome-mcp-servers)
- [A list of MCPs by mudler](https://github.com/mudler/MCPs)

## Client-Side MCP (Browser)

In addition to server-side MCP (where the backend connects to MCP servers), LocalAI supports **client-side MCP** where the browser connects directly to MCP servers. This is inspired by llama.cpp's WebUI and works alongside server-side MCP.

### How It Works

1. **Add servers in the UI**: Click the "Client MCP" button in the chat header and add MCP server URLs
2. **Browser connects directly**: The browser uses the MCP TypeScript SDK (`StreamableHTTPClientTransport` or `SSEClientTransport`) to connect to MCP servers
3. **Tool discovery**: Connected servers' tools are sent as `tools` in the chat request body
4. **Browser-side execution**: When the LLM calls a client-side tool, the browser executes it against the MCP server and sends the result back in a follow-up request
5. **Agentic loop**: This continues (up to 10 turns) until the LLM produces a final response

### CORS Proxy

Since browsers enforce CORS restrictions, LocalAI provides a built-in proxy at `/api/cors-proxy`. When "Use CORS proxy" is enabled (default), requests to external MCP servers are routed through:

```
/api/cors-proxy?url=https://remote-mcp-server.example.com/sse
```

The proxy forwards the request method, headers, and body to the target URL and streams the response back with appropriate CORS headers.

### MCP Apps (Interactive Tool UIs)

LocalAI supports the [MCP Apps extension](https://modelcontextprotocol.io/extensions/apps/overview), which allows MCP tools to declare interactive HTML UIs. When a tool has `_meta.ui.resourceUri` in its definition, calling that tool renders the app's HTML inline in the chat as a sandboxed iframe.

**How it works:**

- When the LLM calls a tool with `_meta.ui.resourceUri`, the browser fetches the HTML resource from the MCP server and renders it in an iframe
- The iframe is sandboxed (`allow-scripts allow-forms`, no `allow-same-origin`) for security
- The app can call server tools, send messages, and update context via the `AppBridge` protocol (JSON-RPC over `postMessage`)
- Tools marked as app-only (`_meta.ui.visibility: "app-only"`) are hidden from the LLM and only callable by the app iframe
- On page reload, apps render statically until the MCP connection is re-established

**Requirements:**

- Only works with **client-side MCP** connections (the browser must be connected to the MCP server)
- The MCP server must implement the Apps extension (`_meta.ui.resourceUri` on tools, resource serving)

### Coexistence with Server-Side MCP

Both modes work simultaneously in the same chat:

- **Server-side MCP tools** are configured in model YAML files and executed by the backend. The backend handles these in its own agentic loop.
- **Client-side MCP tools** are configured per-user in the browser and sent as `tools` in the request. When the LLM calls them, the browser executes them.

If both sides have a tool with the same name, the server-side tool takes priority.

### Security Considerations

- The CORS proxy can forward requests to any HTTP/HTTPS URL. It is only available when MCP is enabled (`LOCALAI_DISABLE_MCP` is not set).
- Client-side MCP server configurations are stored in the browser's localStorage and are not shared with the server.
- Custom headers (e.g., API keys) for MCP servers are stored in localStorage. Use with caution on shared machines.

## Disabling MCP Support

You can completely disable MCP functionality in LocalAI by setting the `LOCALAI_DISABLE_MCP` environment variable to `true`, `1`, or `yes`:

```bash
export LOCALAI_DISABLE_MCP=true
```

When this environment variable is set, all MCP-related features will be disabled, including:
- MCP server connections (both remote and stdio)
- Agent tool execution
- The `/mcp/v1/chat/completions` endpoint

This is useful when you want to:
- Run LocalAI without MCP capabilities for security reasons
- Reduce the attack surface by disabling unnecessary features
- Troubleshoot MCP-related issues

### Example

```bash
# Disable MCP completely
LOCALAI_DISABLE_MCP=true localai run

# Or in Docker
docker run -e LOCALAI_DISABLE_MCP=true localai/localai:latest
```

When MCP is disabled, any model configuration with `mcp` sections will be ignored, and attempts to use the MCP endpoint will return an error indicating that MCP support is disabled.
