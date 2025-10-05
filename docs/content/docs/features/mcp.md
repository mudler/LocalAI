+++
title = "Model Context Protocol (MCP)"
weight = 20
toc = true
description = "Agentic capabilities with Model Context Protocol integration"
tags = ["MCP", "Agents", "Tools", "Advanced"]
categories = ["Features"]
icon = "plug"
+++

# Model Context Protocol (MCP) Support

LocalAI now supports the **Model Context Protocol (MCP)**, enabling powerful agentic capabilities by connecting AI models to external tools and services. This feature allows your LocalAI models to interact with various MCP servers, providing access to real-time data, APIs, and specialized tools.

## What is MCP?

The Model Context Protocol is a standard for connecting AI models to external tools and data sources. It enables AI agents to:

- Access real-time information from external APIs
- Execute commands and interact with external systems
- Use specialized tools for specific tasks
- Maintain context across multiple tool interactions

## Key Features

- **🔄 Real-time Tool Access**: Connect to external MCP servers for live data
- **🛠️ Multiple Server Support**: Configure both remote HTTP and local stdio servers
- **⚡ Cached Connections**: Efficient tool caching for better performance
- **🔒 Secure Authentication**: Support for bearer token authentication
- **🎯 OpenAI Compatible**: Uses the familiar `/mcp/v1/chat/completions` endpoint

## Configuration

MCP support is configured in your model's YAML configuration file using the `mcp` section:

```yaml
name: my-agentic-model
backend: llama-cpp
parameters:
  model: qwen3-4b.gguf

# MCP Configuration
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

## Usage

### API Endpoint

Use the MCP-enabled completion endpoint:

```bash
curl http://localhost:8080/mcp/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-agentic-model",
    "messages": [
      {"role": "user", "content": "What is the current weather in New York?"}
    ],
    "temperature": 0.7
  }'
```

### Example Response

```json
{
  "id": "chatcmpl-123",
  "created": 1699123456,
  "model": "my-agentic-model",
  "choices": [
    {
      "text": "The current weather in New York is 72°F (22°C) with partly cloudy skies. The humidity is 65% and there's a light breeze from the west at 8 mph."
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
```

## How It Works

1. **Tool Discovery**: LocalAI connects to configured MCP servers and discovers available tools
2. **Tool Caching**: Tools are cached per model for efficient reuse
3. **Agent Execution**: The AI model uses the [Cogito](https://github.com/mudler/cogito) framework to execute tools
4. **Response Generation**: The model generates responses incorporating tool results

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
    base_url="http://localhost:8080/mcp/v1",
    api_key="your-api-key"
)

response = client.chat.completions.create(
    model="my-agentic-model",
    messages=[
        {"role": "user", "content": "Analyze the latest research papers on AI"}
    ]
)
```
