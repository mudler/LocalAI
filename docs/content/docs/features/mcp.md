+++
title = "Model Context Protocol (MCP)"
weight = 20
toc = true
description = "Agentic capabilities with Model Context Protocol integration"
tags = ["MCP", "Agents", "Tools", "Advanced"]
categories = ["Features"]
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
- **🧠 Advanced Reasoning**: Configurable reasoning and re-evaluation capabilities
- **⚙️ Flexible Agent Control**: Customizable execution limits and retry behavior

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

# Agent Configuration
agent:
  max_attempts: 3        # Maximum number of tool execution attempts
  max_iterations: 3     # Maximum number of reasoning iterations
  enable_reasoning: true # Enable tool reasoning capabilities
  enable_re_evaluation: false # Enable tool re-evaluation
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
Configure agent behavior and tool execution:

- **`max_attempts`**: Maximum number of tool execution attempts (default: 3)
- **`max_iterations`**: Maximum number of reasoning iterations (default: 3)
- **`enable_reasoning`**: Enable tool reasoning capabilities (default: false)
- **`enable_re_evaluation`**: Enable tool re-evaluation (default: false)

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

agent:
  max_attempts: 5
  max_iterations: 5
  enable_reasoning: true
  enable_re_evaluation: true
```

## Agent Configuration Details

The `agent` section controls how the AI model interacts with MCP tools:

### Execution Control
- **`max_attempts`**: Limits how many times a tool can be retried if it fails. Higher values provide more resilience but may increase response time.
- **`max_iterations`**: Controls the maximum number of reasoning cycles the agent can perform. More iterations allow for complex multi-step problem solving.

### Reasoning Capabilities
- **`enable_reasoning`**: When enabled, the agent uses advanced reasoning to better understand tool results and plan next steps.
- **`enable_re_evaluation`**: When enabled, the agent can re-evaluate previous tool results and decisions, allowing for self-correction and improved accuracy.

### Recommended Settings
- **Simple tasks**: `max_attempts: 2`, `max_iterations: 2`, `enable_reasoning: false`
- **Complex tasks**: `max_attempts: 5`, `max_iterations: 5`, `enable_reasoning: true`, `enable_re_evaluation: true`
- **Development/Debugging**: `max_attempts: 1`, `max_iterations: 1`, `enable_reasoning: true`, `enable_re_evaluation: true`

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

### MCP and adding packages

It might be handy to install packages before starting the container to setup the environment. This is an example on how you can do that with docker-compose (installing and configuring docker)

```yaml
services:
  local-ai:
    image: localai/localai:latest
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
            "MEMORY_FILE_PATH": "/data/memory.json"
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
