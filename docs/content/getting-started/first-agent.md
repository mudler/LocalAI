+++
disableToc = false
title = "Build your first agent"
weight = 6
icon = "smart_toy"
+++

LocalAGI is embedded in LocalAI. There is nothing separate to install or run.

The agent platform ships inside the LocalAI binary and container image, and it is enabled by default. If you already have LocalAI running and a model installed, you have everything you need to build an agent. This page walks you from an empty Agents page to an agent that answers a message and uses one tool.

## Before you start: install a tool-calling model

An agent is a loop around a chat model, so it needs a model that supports tool (function) calling. This guide uses `qwen3-4b`, a small CPU-friendly Qwen3 model that supports tool calling. It is the same model used in the [Quickstart]({{% relref "getting-started/quickstart" %}}), so if you followed that page you already have it.

Install it either from the web interface or from the CLI:

- **Web interface:** open the **Models** page at `http://localhost:8080`, search for `qwen3-4b`, and click **Install**.
- **CLI:**

```bash
local-ai run qwen3-4b
```

For other ways to install models (Hugging Face, OCI, local files), see the [model gallery]({{% relref "features/model-gallery" %}}).

## Give the agent a model

An agent with no model set cannot answer. The agent has nothing to send your message to, so it will fail to respond until you assign it a model. You can set the model in two ways:

- **Per agent:** choose `qwen3-4b` in the agent's **Model** field when you create or edit it (covered below). This is the usual choice.
- **As a default for every new agent:** start LocalAI with an environment variable so new agents are created with that model already selected:

```bash
LOCALAI_AGENT_POOL_DEFAULT_MODEL=qwen3-4b
```

Setting a per-agent model always overrides the default.

## Create the agent

1. Open the **Agents** page in the web interface.
2. Click **Create Agent**.
3. Fill in the form:
   - **Name:** for example `helper`.
   - **Model:** select `qwen3-4b`.
   - **System prompt:** a short instruction that sets the agent's behavior, for example `You are a concise, helpful assistant.`
   - **Action:** add one simple action so the agent has a tool to call. A search action is a good first choice. Some actions need credentials (for example an API key); pick one whose requirements you can satisfy, or start with an action that needs none.
4. Save the agent.

## Send a message

Open the new agent from the Agents page and type a message in its chat box, for example `Hello, what can you do?`. The agent replies in the chat panel within a few seconds. When the agent decides to use the action you configured, you will see the tool call and its result appear inline before the final answer, streamed live as the agent works.

That is a complete agent: a model, a system prompt, and one tool, all running inside your LocalAI process.

## If your imported agent will not run

If you imported an agent from the [Agent Hub](https://agenthub.localai.io) or a JSON file and it does not respond, work through this checklist. Each symptom maps to a fix:

- **The agent does not answer at all:** the model it references is not installed. Open the **Models** page and install the model named in the agent's configuration (or change the agent's model to one you have, such as `qwen3-4b`).
- **An action always fails:** the action is missing its API keys or other credentials. Open the agent's action configuration and supply the required keys.
- **A tool times out or is unavailable:** the MCP server that provides it is unreachable. Confirm the MCP server is running and that the agent points at the correct address.
- **A skill the agent expects is not available:** skills are disabled by default. Start LocalAI with `LOCALAI_AGENT_POOL_ENABLE_SKILLS=true` to turn the skills service on (the default is `LOCALAI_AGENT_POOL_ENABLE_SKILLS=false`).

## Next steps

- [Model gallery]({{% relref "features/model-gallery" %}}) - install more models, including larger tool-calling models for more capable agents.
- [Agent actions catalog]({{% relref "features/agent-actions" %}}) - the full list of built-in actions an agent can use and how to configure them.
- [Agent-scoped MCP]({{% relref "features/mcp" %}}) - connect an agent to external Model Context Protocol servers to give it more tools.
