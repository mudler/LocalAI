+++
disableToc = false
title = "🤖 Agents"
weight = 21
url = '/features/agents'
+++

LocalAI includes a built-in agent platform powered by [LocalAGI](https://github.com/mudler/LocalAGI). Agents are autonomous AI entities that can reason, use tools, maintain memory, and interact with external services — all running locally as part of the LocalAI process.

## Overview

The agent system provides:

- **Autonomous agents** with configurable goals, personalities, and capabilities
- **Tool/Action support** — agents can execute actions (web search, code execution, API calls, etc.)
- **Knowledge base (RAG)** — per-agent collections with document upload, chunking, and semantic search
- **Skills system** — reusable skill definitions that agents can leverage, with git-based skill repositories
- **SSE streaming** — real-time chat with agents via Server-Sent Events
- **Import/Export** — share agent configurations as JSON files
- **Agent Hub** — browse and download ready-made agents from [agenthub.localai.io](https://agenthub.localai.io)
- **Web UI** — full management interface for creating, editing, chatting with, and monitoring agents

## Getting Started

Agents are enabled by default. To disable them, set:

```bash
LOCALAI_DISABLE_AGENTS=true
```

### Creating an Agent

1. Navigate to the **Agents** page in the web UI
2. Click **Create Agent** or import one from the [Agent Hub](https://agenthub.localai.io)
3. Configure the agent's name, model, system prompt, and actions
4. Save and start chatting

### Importing an Agent

You can import agent configurations from JSON files:

1. Download an agent configuration from the [Agent Hub](https://agenthub.localai.io) or export one from another LocalAI instance
2. On the **Agents** page, click **Import**
3. Select the JSON file — you'll be taken to the edit form to review and adjust the configuration before saving
4. Click **Create Agent** to finalize the import

## Configuration

### Environment Variables

All agent-related settings can be configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `LOCALAI_DISABLE_AGENTS` | `false` | Disable the agent pool feature entirely |
| `LOCALAI_AGENT_POOL_API_URL` | _(self-referencing)_ | Default API URL for agents. By default, agents call back into LocalAI's own API (`http://127.0.0.1:<port>`). Set this to point agents to an external LLM provider. |
| `LOCALAI_AGENT_POOL_API_KEY` | _(LocalAI key)_ | Default API key for agents. Defaults to the first LocalAI API key. Set this when using an external provider. |
| `LOCALAI_AGENT_POOL_DEFAULT_MODEL` | _(empty)_ | Default LLM model for new agents |
| `LOCALAI_AGENT_POOL_MULTIMODAL_MODEL` | _(empty)_ | Default multimodal (vision) model for agents |
| `LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL` | _(empty)_ | Default transcription (speech-to-text) model for agents |
| `LOCALAI_AGENT_POOL_TRANSCRIPTION_LANGUAGE` | _(empty)_ | Default transcription language for agents |
| `LOCALAI_AGENT_POOL_TTS_MODEL` | _(empty)_ | Default TTS (text-to-speech) model for agents |
| `LOCALAI_AGENT_POOL_STATE_DIR` | _(config dir)_ | Directory for persisting agent state |
| `LOCALAI_AGENT_POOL_TIMEOUT` | `5m` | Default timeout for agent operations |
| `LOCALAI_AGENT_POOL_ENABLE_SKILLS` | `false` | Enable the skills service |
| `LOCALAI_AGENT_POOL_VECTOR_ENGINE` | `chromem` | Vector engine for knowledge base (`chromem` or `postgres`) |
| `LOCALAI_AGENT_POOL_EMBEDDING_MODEL` | `granite-embedding-107m-multilingual` | Embedding model for knowledge base |
| `LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR` | _(empty)_ | Directory for custom action plugins |
| `LOCALAI_AGENT_POOL_DATABASE_URL` | _(empty)_ | PostgreSQL connection string for collections (required when vector engine is `postgres`) |
| `LOCALAI_AGENT_POOL_MAX_CHUNKING_SIZE` | `400` | Maximum chunk size for document ingestion |
| `LOCALAI_AGENT_POOL_CHUNK_OVERLAP` | `0` | Overlap between document chunks |
| `LOCALAI_AGENT_POOL_ENABLE_LOGS` | `false` | Enable detailed agent logging |
| `LOCALAI_AGENT_POOL_COLLECTION_DB_PATH` | _(empty)_ | Custom path for the collections database |
| `LOCALAI_AGENT_HUB_URL` | `https://agenthub.localai.io` | URL for the Agent Hub (shown in the UI) |

### Knowledge Base Storage

By default, the knowledge base uses **chromem** — an in-process vector store that requires no external dependencies. For production deployments with larger knowledge bases, you can switch to **PostgreSQL** with pgvector support:

```bash
LOCALAI_AGENT_POOL_VECTOR_ENGINE=postgres
LOCALAI_AGENT_POOL_DATABASE_URL=postgresql://localrecall:localrecall@postgres:5432/localrecall?sslmode=disable
```

The PostgreSQL image `quay.io/mudler/localrecall:v0.5.2-postgresql` is pre-configured with pgvector and ready to use.

### Docker Compose Example

Basic setup with in-memory vector store:

```yaml
services:
  localai:
    image: localai/localai:latest
    ports:
      - 8080:8080
    environment:
      - MODELS_PATH=/models
      - LOCALAI_AGENT_POOL_DEFAULT_MODEL=hermes-3-llama3.1-8b
      - LOCALAI_AGENT_POOL_EMBEDDING_MODEL=granite-embedding-107m-multilingual
      - LOCALAI_AGENT_POOL_ENABLE_SKILLS=true
      - LOCALAI_AGENT_POOL_ENABLE_LOGS=true
    volumes:
      - models:/models
      - localai_config:/etc/localai
volumes:
  models:
  localai_config:
```

Setup with PostgreSQL for persistent knowledge base:

```yaml
services:
  localai:
    image: localai/localai:latest
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - 8080:8080
    environment:
      - MODELS_PATH=/models
      - LOCALAI_AGENT_POOL_DEFAULT_MODEL=hermes-3-llama3.1-8b
      - LOCALAI_AGENT_POOL_EMBEDDING_MODEL=granite-embedding-107m-multilingual
      - LOCALAI_AGENT_POOL_ENABLE_SKILLS=true
      - LOCALAI_AGENT_POOL_ENABLE_LOGS=true
      # PostgreSQL-backed knowledge base
      - LOCALAI_AGENT_POOL_VECTOR_ENGINE=postgres
      - LOCALAI_AGENT_POOL_DATABASE_URL=postgresql://localrecall:localrecall@postgres:5432/localrecall?sslmode=disable
    volumes:
      - models:/models
      - localai_config:/etc/localai

  postgres:
    image: quay.io/mudler/localrecall:v0.5.2-postgresql
    environment:
      - POSTGRES_DB=localrecall
      - POSTGRES_USER=localrecall
      - POSTGRES_PASSWORD=localrecall
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U localrecall"]
      interval: 10s
      timeout: 5s
      retries: 5

volumes:
  models:
  localai_config:
  postgres_data:
```

## Agent Configuration

Each agent has its own configuration that controls its behavior. Key settings include:

- **Name** — unique identifier for the agent
- **Model** — the LLM model the agent uses for reasoning
- **System Prompt** — defines the agent's personality and instructions
- **Actions** — tools the agent can use (web search, code execution, etc.)
- **Connectors** — external integrations (Slack, Discord, etc.)
- **Knowledge Base** — collections of documents for RAG
- **MCP Servers** — Model Context Protocol servers for additional tool access

The pool-level defaults (API URL, API key, models) can be set via environment variables. Individual agents can further override these in their configuration, allowing them to use different LLM providers (OpenAI, other LocalAI instances, etc.) on a per-agent basis.

## API Endpoints

All agent endpoints are grouped under `/api/agents/`:

### Agent Management

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/agents` | List all agents with status |
| `POST` | `/api/agents` | Create a new agent |
| `GET` | `/api/agents/:name` | Get agent info |
| `PUT` | `/api/agents/:name` | Update agent configuration |
| `DELETE` | `/api/agents/:name` | Delete an agent |
| `GET` | `/api/agents/:name/config` | Get agent configuration |
| `PUT` | `/api/agents/:name/pause` | Pause an agent |
| `PUT` | `/api/agents/:name/resume` | Resume a paused agent |
| `GET` | `/api/agents/:name/status` | Get agent status and observables |
| `POST` | `/api/agents/:name/chat` | Send a message to an agent |
| `GET` | `/api/agents/:name/sse` | SSE stream for real-time agent events |
| `GET` | `/api/agents/:name/export` | Export agent configuration as JSON |
| `POST` | `/api/agents/import` | Import an agent from JSON |
| `GET` | `/api/agents/config/metadata` | Get dynamic config form metadata |

### Skills

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/agents/skills` | List all skills |
| `POST` | `/api/agents/skills` | Create a new skill |
| `GET` | `/api/agents/skills/:name` | Get a skill |
| `PUT` | `/api/agents/skills/:name` | Update a skill |
| `DELETE` | `/api/agents/skills/:name` | Delete a skill |
| `GET` | `/api/agents/skills/search` | Search skills |
| `GET` | `/api/agents/skills/export/*` | Export a skill |
| `POST` | `/api/agents/skills/import` | Import a skill |

### Collections (Knowledge Base)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/agents/collections` | List collections |
| `POST` | `/api/agents/collections` | Create a collection |
| `POST` | `/api/agents/collections/:name/upload` | Upload a document |
| `GET` | `/api/agents/collections/:name/entries` | List entries |
| `POST` | `/api/agents/collections/:name/search` | Search a collection |
| `POST` | `/api/agents/collections/:name/reset` | Reset a collection |

### Actions

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/agents/actions` | List available actions |
| `POST` | `/api/agents/actions/:name/definition` | Get action definition |
| `POST` | `/api/agents/actions/:name/run` | Execute an action |

## Using Agents via the Responses API

Agents can be used programmatically via the standard `/v1/responses` endpoint (OpenAI Responses API). Simply use the agent name as the `model` field:

```bash
curl -X POST http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-agent",
    "input": "What is the weather today?"
  }'
```

This returns a standard Responses API response:

```json
{
  "id": "resp_...",
  "object": "response",
  "status": "completed",
  "model": "my-agent",
  "output": [
    {
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "output_text",
          "text": "The agent's response..."
        }
      ]
    }
  ]
}
```

You can also send structured message arrays as input:

```bash
curl -X POST http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-agent",
    "input": [
      {"role": "user", "content": "Summarize the latest news about AI"}
    ]
  }'
```

When the model name matches an agent, the request is routed to the agent pool. If no agent matches, it falls through to the normal model-based inference pipeline.

## Chat with SSE Streaming

For real-time streaming responses, use the chat endpoint with SSE:

Send a message to an agent:

```bash
curl -X POST http://localhost:8080/api/agents/my-agent/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "What is the weather today?"}'
```

Listen to real-time events via SSE:

```bash
curl -N http://localhost:8080/api/agents/my-agent/sse
```

The SSE stream emits the following event types:

- `json_message` — agent/user messages
- `json_message_status` — processing status updates (`processing` / `completed`)
- `status` — system messages (reasoning steps, action results)
- `json_error` — error notifications

## Standalone Agent CLI

You can run an agent outside of the full LocalAI server using the `agent run` CLI command. This launches a single agent in standalone mode, which is useful for scripting, CI/CD pipelines, or running agents without the HTTP server.

### Usage

```bash
# Run from a local JSON configuration file
local-ai agent run ./my-agent.json

# Run from the Agent Hub registry by name
local-ai agent run my-agent-name

# Override API URL and model
local-ai agent run ./my-agent.json --api-url http://localhost:8080 --default-model llama3
```

### Arguments

| Argument | Description |
|----------|-------------|
| `<agent-ref>` | **(required)** Agent name from the registry or path to a JSON config file |

### Flags

| Flag | Env Variable | Default | Description |
|------|-------------|---------|-------------|
| `--api-url` | `LOCALAI_AGENT_POOL_API_URL` | | API URL the agent uses for LLM inference |
| `--api-key` | `LOCALAI_AGENT_POOL_API_KEY` | | API key for the agent |
| `--default-model` | `LOCALAI_AGENT_POOL_DEFAULT_MODEL` | | Default model for the agent |
| `--multimodal-model` | `LOCALAI_AGENT_POOL_MULTIMODAL_MODEL` | | Multimodal model |
| `--transcription-model` | `LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL` | | Transcription model |
| `--tts-model` | `LOCALAI_AGENT_POOL_TTS_MODEL` | | TTS model |
| `--state-dir` | `LOCALAI_AGENT_POOL_STATE_DIR` | `agent-state` | State directory |
| `--timeout` | `LOCALAI_AGENT_POOL_TIMEOUT` | `5m` | Agent timeout |
| `--enable-skills` | `LOCALAI_AGENT_POOL_ENABLE_SKILLS` | `false` | Enable skills service |
| `--enable-logs` | `LOCALAI_AGENT_POOL_ENABLE_LOGS` | `false` | Enable agent logging |
| `--agent-hub-url` | `LOCALAI_AGENT_HUB_URL` | `https://agenthub.localai.io` | Agent Hub URL for registry lookups |

### How It Works

1. **File reference**: If the argument is a path to an existing file or ends in `.json`, the agent config is loaded from that file.
2. **Registry lookup**: Otherwise, the agent name is looked up from the Agent Hub registry (`GET <hub-url>/api/agents/<name>`).
3. CLI flags fill in any values not already set in the configuration (e.g., `--api-url` sets the API URL only if the config doesn't already specify one).
4. The agent is created and started in standalone mode. Press `Ctrl+C` to stop.

### Example Agent Config

```json
{
  "name": "my-assistant",
  "model": "hermes-3-llama3.1-8b",
  "api_url": "http://localhost:8080",
  "system_prompt": "You are a helpful assistant.",
  "standalone_job": true,
  "actions": [
    {"name": "search", "config": "{}"}
  ]
}
```

## Architecture

Agents run in-process within LocalAI. By default, each agent calls back into LocalAI's own API (`http://127.0.0.1:<port>/v1/chat/completions`) for LLM inference. This means:

- No external dependencies — everything runs in a single binary
- Agents use the same models loaded in LocalAI
- Per-agent overrides allow pointing individual agents to external providers
- Agent state is persisted to disk and restored on restart

```
User → POST /api/agents/:name/chat → LocalAI
  → AgentPool → Agent reasoning loop
    → POST /v1/chat/completions (self-referencing)
      → LocalAI model inference → response
        → SSE events → GET /api/agents/:name/sse → UI
```
