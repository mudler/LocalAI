+++
disableToc = false
title = "Agents"
weight = 20
url = '/features/agents'
+++

![The in-process agent loop: agents call LocalAI's own chat API in a loop, streaming progress over SSE](/images/diagrams/agents-loop.png)

LocalAI includes a built-in agent platform powered by [LocalAGI](https://github.com/mudler/LocalAGI). Agents are autonomous AI entities that can reason, use tools, maintain memory, and interact with external services, all running locally as part of the LocalAI process.

LocalAGI is embedded in LocalAI. There is nothing separate to install or run.

{{% notice info %}}
**Looking for something else?** LocalAI has three related agentic features that are easy to confuse:

- **Agents** (this page): autonomous agents you build that reason, use tools, and act on their own. Start here if you want to create an agent.
- **LocalAI Assistant** ({{% relref "features/localai-assistant" %}}): an admin chat modality for administering LocalAI itself (install models, manage backends) by chatting.
- **MCP** ({{% relref "features/mcp" %}}): a way to give a model external tools through the Model Context Protocol.
{{% /notice %}}

{{% notice tip %}}
New to agents? The [Build your first agent]({{% relref "getting-started/first-agent" %}}) walkthrough takes you from an empty Agents page to an agent that answers a message and uses one tool.
{{% /notice %}}

## Overview

The agent system provides:

- **Autonomous agents** with configurable goals, personalities, and capabilities
- **Tool/Action support** - agents can execute actions (web search, code execution, API calls, etc.)
- **Knowledge base (RAG)** - per-agent collections with document upload, chunking, and semantic search
- **Skills system** - reusable skill definitions that agents can leverage, with git-based skill repositories
- **SSE streaming** - real-time chat with agents via Server-Sent Events
- **Import/Export** - share agent configurations as JSON files
- **Agent Hub** - browse and download ready-made agents from [agenthub.localai.io](https://agenthub.localai.io)
- **Web UI** - full management interface for creating, editing, chatting with, and monitoring agents

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
3. Select the JSON file - you'll be taken to the edit form to review and adjust the configuration before saving
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
| `LOCALAI_AGENT_POOL_STATE_DIR` | _(data path)_ | Directory for persisting agent state. Defaults to `LOCALAI_DATA_PATH` if set, otherwise falls back to `LOCALAI_CONFIG_DIR` |
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

By default, the knowledge base uses **chromem** - an in-process vector store that requires no external dependencies. For production deployments with larger knowledge bases, you can switch to **PostgreSQL** with pgvector support:

```bash
LOCALAI_AGENT_POOL_VECTOR_ENGINE=postgres
LOCALAI_AGENT_POOL_DATABASE_URL=postgresql://localrecall:localrecall@postgres:5432/localrecall?sslmode=disable
```

The PostgreSQL image `quay.io/mudler/localrecall:v0.5.2-postgresql` is pre-configured with pgvector and ready to use.

#### Connection safety timeouts (PostgreSQL only)

The embedded vector store sets per-connection timeouts so a single stuck or corrupt index can never hold a lock indefinitely and stall every other collection operation. Safe defaults are applied automatically - you only need to set these to override them:

| Variable | Default | Description |
|----------|---------|-------------|
| `POSTGRES_LOCK_TIMEOUT` | `30s` | Bounds how long a statement waits to acquire a lock, so queued statements fail fast instead of piling up. Set `0`/`off` to disable. |
| `POSTGRES_IDLE_IN_TRANSACTION_TIMEOUT` | `300s` | Reaps abandoned transactions that would otherwise pin locks. Set `0`/`off` to disable. |
| `POSTGRES_STATEMENT_TIMEOUT` | _(unset)_ | Bounds total statement runtime, auto-aborting a wedged query. Off by default since a large vector index build can exceed any fixed limit; index builds are exempted, so it is safe to enable. |

These are read directly from the LocalAI process environment by the embedded store (the same as `DATABASE_URL` and `HYBRID_SEARCH_*`).

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
      - LOCALAI_DATA_PATH=/data
      - LOCALAI_AGENT_POOL_DEFAULT_MODEL=hermes-3-llama3.1-8b
      - LOCALAI_AGENT_POOL_EMBEDDING_MODEL=granite-embedding-107m-multilingual
      - LOCALAI_AGENT_POOL_ENABLE_SKILLS=true
      - LOCALAI_AGENT_POOL_ENABLE_LOGS=true
    volumes:
      - models:/models
      - localai_data:/data
      - localai_config:/etc/localai
volumes:
  models:
  localai_data:
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

- **Name** - unique identifier for the agent
- **Model** - the LLM model the agent uses for reasoning
- **System Prompt** - defines the agent's personality and instructions
- **Actions** - tools the agent can use (web search, code execution, etc.). See the {{% relref "features/agent-actions" %}} for the full catalog of built-in actions and how to configure each one.
- **Connectors** - external integrations (Slack, Discord, etc.)
- **Knowledge Base** - collections of documents for RAG
- **MCP Servers** - Model Context Protocol servers for additional tool access

The pool-level defaults (API URL, API key, models) can be set via environment variables. Individual agents can further override these in their configuration, allowing them to use different LLM providers (OpenAI, other LocalAI instances, etc.) on a per-agent basis.

## Skills

Skills are reusable instruction sets (a name, a description, and the skill's content, optionally with attached resource files) that an agent can draw on while it works. They can be authored directly or imported from git-based skill repositories, so a set of skills can be shared across agents and machines.

{{% notice warning %}}
Skills are **disabled by default**. The skills service only runs when you start LocalAI with `LOCALAI_AGENT_POOL_ENABLE_SKILLS=true`. With the default `LOCALAI_AGENT_POOL_ENABLE_SKILLS=false`, the Skills UI and the `/api/agents/skills` endpoints are inactive, and an imported agent that expects a skill will not find it.
{{% /notice %}}

### Enable skills

Start LocalAI with the skills service turned on:

```bash
LOCALAI_AGENT_POOL_ENABLE_SKILLS=true local-ai run
```

In Docker, add the same variable to the container environment.

### Create a skill

With skills enabled, open the **Agents** section in the web interface and go to the Skills area. Create a skill by giving it:

- a **name** (used to reference the skill),
- a **description** (what the skill is for),
- the skill **content** (the instructions the agent follows when the skill applies),
- optionally, resource files the skill needs.

The same operation is available over REST:

```bash
curl http://localhost:8080/api/agents/skills \
  -H "Content-Type: application/json" \
  -d '{
    "name": "changelog-writer",
    "description": "Write a release changelog from a list of merged PRs",
    "content": "When asked for a changelog, group the PRs by type (feature, fix, docs) and write one concise bullet per PR."
  }'
```

You can also import a skill archive with `POST /api/agents/skills/import`, or add a git skill repository so its skills are pulled in.

### Use a skill

Once a skill exists and the skills service is enabled, agents can use it as part of their reasoning. List the skills currently available with:

```bash
curl http://localhost:8080/api/agents/skills
```

If a skill you expect is missing, confirm LocalAI was started with `LOCALAI_AGENT_POOL_ENABLE_SKILLS=true`.

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
| `GET` | `/api/agents/:name/files?path=...` | Serve a generated file from the outputs directory |
| `GET` | `/api/agents/config/metadata` | Get dynamic config form metadata (includes `outputsDir`) |

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

- `json_message` - agent/user messages
- `json_message_status` - processing status updates (`processing` / `completed`)
- `status` - system messages (reasoning steps, action results)
- `json_error` - error notifications

## Generated Files and Outputs

Some agent actions (image generation, PDF creation, audio synthesis) produce files. These files are automatically managed by LocalAI through a confined **outputs directory**.

### How It Works

1. Actions generate files to their configured `outputDir` (which can be any path on the filesystem)
2. After each agent response, LocalAI automatically copies generated files into `{stateDir}/outputs/`
3. The file-serving endpoint (`/api/agents/:name/files?path=...`) only serves files from this outputs directory
4. File paths in agent response metadata are rewritten to point to the copied files

This design ensures that:
- Actions can write files to any directory they need
- The file-serving endpoint is confined to a single trusted directory - no arbitrary filesystem access
- Symlink traversal is blocked via `filepath.EvalSymlinks` validation

### Accessing Generated Files

Use the file-serving endpoint to retrieve files produced by agent actions:

```bash
curl http://localhost:8080/api/agents/my-agent/files?path=/path/to/outputs/image.png
```

The `path` parameter must point to a file inside the outputs directory. Requests for files outside this directory are rejected with `403 Forbidden`.

### Metadata in SSE Messages

When an agent action produces files, the SSE `json_message` event includes a `metadata` field with the generated resources:

```json
{
  "id": "msg-123-agent",
  "sender": "agent",
  "content": "Here is the image you requested.",
  "metadata": {
    "images_url": ["http://localhost:8080/api/agents/my-agent/files?path=..."],
    "pdf_paths": ["/path/to/outputs/document.pdf"],
    "songs_paths": ["/path/to/outputs/song.mp3"]
  },
  "timestamp": "2025-01-01T00:00:00Z"
}
```

The web UI uses this metadata to display inline resource cards (images, PDFs, audio players) and to open files in the canvas panel.

### Configuration

The outputs directory is created at `{stateDir}/outputs/` where `stateDir` defaults to `LOCALAI_AGENT_POOL_STATE_DIR` (or `LOCALAI_DATA_PATH` / `LOCALAI_CONFIG_DIR` as fallbacks). You can query the current outputs directory path via:

```bash
curl http://localhost:8080/api/agents/config/metadata
```

This returns a JSON object including the `outputsDir` field.

## Architecture

Agents run in-process within LocalAI. By default, each agent calls back into LocalAI's own API (`http://127.0.0.1:<port>/v1/chat/completions`) for LLM inference. This means:

- No external dependencies - everything runs in a single binary
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
