+++
disableToc = false
title = "Connect coding agents"
weight = 23
url = '/features/coding-agents'
+++

Coding agents such as Codex CLI, Claude Code and opencode talk to a model over HTTP.
LocalAI exposes the three APIs they use, so you can point them at your own server
and keep your code on your own hardware.

This page shows the smallest working setup for each agent. All examples use
`http://localhost:8080` as the server address and `sk-localai` as a placeholder key.
Replace both with your own values.

The agent settings below were checked against each project's documentation or source
in November 2026. Agents change fast. If a key is rejected, check the agent's own
docs for the current name.

## What LocalAI provides

| API | Endpoint | Used by |
|---|---|---|
| OpenAI Chat Completions | `POST /v1/chat/completions` | opencode, nib, Hermes Agent, pi, Aider, Continue, Cline, Goose |
| OpenAI Responses | `POST /v1/responses` (also `/responses`) | Codex CLI |
| Anthropic Messages | `POST /v1/messages` (also `/messages`) | Claude Code |

The default port is `8080`. Set it with `--address` or `LOCALAI_ADDRESS`.

Model list: `GET /v1/models` returns the names you can use as the `model` value.

### Authentication

If you start LocalAI without an API key, the API is open and any key value works.
If you set `LOCALAI_API_KEY`, every request needs a valid key. LocalAI reads the key
from the `Authorization: Bearer <key>` header, the `x-api-key` header or the
`xi-api-key` header. Claude Code sends its token in one of the first two,
so both work. See [Authentication]({{% relref "features/authentication" %}}).

### Model names

The agent sends the model name you give it. LocalAI looks it up in your installed
models and returns `404` with `model "<name>" not found` when it does not exist.
If a request has no model name at all, LocalAI uses the first available chat model.

Some agents hard-code a model name, or send a second "small" model name for background
tasks. Use a [model alias]({{% relref "features/model-aliases" %}}) to map such a name
to one of your models.

## What the model needs

A coding agent is mostly a loop of tool calls: read a file, run a command, edit a file.
Not every model can do this.

**Tool calling.** The model must be trained for tool calls, and LocalAI must be able to
parse its output. For GGUF models on the llama.cpp backend, use the model's own chat
template (`template.use_tokenizer_template: true`). LocalAI then detects the tool call
format. Models installed from the gallery are usually set up this way. See
[OpenAI Functions and Tools]({{% relref "features/openai-functions" %}}) for the vLLM and
MLX backends.

LocalAI does not reject a request that has tools when the model has no tool support.
The tools are put in the prompt, the model answers with plain text, and the agent sees
no tool call. The usual symptom is an agent that describes what it would do and never
does it. If you see this, switch to a model with tool support.

**Context size.** Agents send a long system prompt and tool descriptions before your
first message. Use at least 32K tokens of context. Hermes Agent refuses to start with
less than 64K, and the other agents behave better with that size too. Set it per model
with `context_size` in the model YAML, or set a global default with `--context-size`
(`LOCALAI_CONTEXT_SIZE`). If you do not set it, LocalAI picks a size from the GGUF
metadata and the memory it detects, which can be smaller than you want. A larger
context needs more memory for the KV cache.

**Streaming.** All the agents below stream responses. The three endpoints support it.

## Check that it works

List the models, then send one chat request:

```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer sk-localai"

curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-localai" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-model",
    "messages": [{"role": "user", "content": "What is the weather in Rome?"}],
    "tools": [{
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get the weather for a city",
        "parameters": {
          "type": "object",
          "properties": {"city": {"type": "string"}},
          "required": ["city"]
        }
      }
    }]
  }'
```

A model with tool support returns a `tool_calls` entry for `get_weather` in the
first choice. If it returns only text, the agents will not work well with this model.

To test the other two endpoints:

```bash
curl http://localhost:8080/v1/responses \
  -H "Authorization: Bearer sk-localai" \
  -H "Content-Type: application/json" \
  -d '{"model": "my-model", "input": "Say hello"}'

curl http://localhost:8080/v1/messages \
  -H "x-api-key: sk-localai" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model": "my-model", "max_tokens": 64, "messages": [{"role": "user", "content": "Say hello"}]}'
```

`max_tokens` is required on `/v1/messages`. LocalAI returns `400` without it.

## Codex CLI

Codex CLI uses the Responses API. Current versions accept only
`wire_api = "responses"`; the older `"chat"` value is no longer supported. LocalAI
serves `/v1/responses`, so Codex works without a proxy.

Add a provider to `~/.codex/config.toml`:

```toml
model = "my-model"
model_provider = "localai"
model_context_window = 65536

[model_providers.localai]
name = "LocalAI"
base_url = "http://localhost:8080/v1"
env_key = "LOCALAI_API_KEY"
wire_api = "responses"
```

Then export the key and start Codex:

```bash
export LOCALAI_API_KEY=sk-localai
codex
```

Endpoint: `/v1/responses`.

Caveat: LocalAI turns only tools of type `function` into model tools. Other Responses
tool types, such as built-in web search, are skipped. Codex does not have metadata for
your model name, so set `model_context_window` yourself to the context size of the model.
Provider definitions must be in the user-level file. Project `.codex/config.toml`
files cannot set `model_providers`.

## Claude Code

Claude Code can use any gateway that serves the Anthropic Messages API, and LocalAI
serves `/v1/messages`. Claude Code adds `/v1/messages` to the base URL, so do not
include `/v1` in `ANTHROPIC_BASE_URL`.

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080
export ANTHROPIC_AUTH_TOKEN=sk-localai
export ANTHROPIC_MODEL=my-model
export ANTHROPIC_DEFAULT_HAIKU_MODEL=my-model
claude
```

`ANTHROPIC_AUTH_TOKEN` is sent as `Authorization: Bearer`. `ANTHROPIC_API_KEY` is sent
as `x-api-key`. LocalAI accepts both. `ANTHROPIC_DEFAULT_HAIKU_MODEL` moves background
tasks such as session titles to your model; without it Claude Code asks for a Claude
model name that your server does not have. To keep the settings, put the same
variables in the `env` block of `~/.claude/settings.json`.

Run `/status` in Claude Code to check that the base URL is the one you set.

Endpoint: `/v1/messages`.

Caveat: Anthropic does not support routing Claude Code to non-Claude models. It works
as far as LocalAI's Messages endpoint covers what Claude Code sends. If requests fail
with `400` and an error about unknown fields, set
`CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1`.

## opencode

opencode reads `opencode.json` or `opencode.jsonc`. The global file is
`~/.config/opencode/opencode.json`. A project can have its own file with the same name.
This example uses the provider format of opencode 2.x. The older `provider`, `npm` and
`options` keys are still accepted and migrated.

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "providers": {
    "localai": {
      "name": "LocalAI",
      "env": ["LOCALAI_API_KEY"],
      "package": "@opencode/ai/providers/openai-compatible",
      "settings": { "baseURL": "http://localhost:8080/v1" },
      "models": {
        "my-model": {
          "name": "My model",
          "limit": { "context": 65536, "output": 8192 }
        }
      }
    }
  }
}
```

Export `LOCALAI_API_KEY`, start `opencode`, and pick `localai/my-model` with `/models`.

Endpoint: `/v1/chat/completions`.

Caveat: set `limit.context` to the real context size of the model. opencode uses it to
decide when to compact the conversation.

## Hermes Agent

Hermes Agent (Nous Research) works with any OpenAI-compatible endpoint. The easy way is
the setup wizard:

```bash
hermes model
# Choose "Custom endpoint", then enter the URL, the key and the model name.
```

To write the config by hand, edit `~/.hermes/config.yaml`:

```yaml
model:
  default: my-model
  provider: custom
  base_url: http://localhost:8080/v1
  api_key: sk-localai
  context_length: 65536
```

You can use `key_env: LOCALAI_API_KEY` instead of `api_key` to read the key from an
environment variable.

Endpoint: `/v1/chat/completions`.

Caveat: Hermes needs a context window of at least 64,000 tokens for agent use and
refuses smaller windows at startup. Set `context_size` for the model in LocalAI to
64000 or more.

## nib

nib is a small terminal agent. It uses any OpenAI-compatible endpoint. Edit
`~/.config/nib/config.yaml`:

```yaml
model: my-model
api_key: sk-localai
base_url: http://localhost:8080/v1
```

You can also name more than one endpoint:

```yaml
endpoints:
  localai:
    base_url: http://localhost:8080/v1
    api_key_env: LOCALAI_API_KEY
    model: my-model
```

Start it with `nib`, or press `Ctrl+Space` in your shell.

Endpoint: `/v1/chat/completions`.

Caveat: nib asks for approval before it runs a tool, unless you set `--yolo`.

## pi

pi (the pi coding agent from pi.dev) keeps custom endpoints in
`~/.pi/agent/models.json`. You can move the folder with `PI_CODING_AGENT_DIR`.

```json
{
  "$schema": "https://pi.dev/schemas/models.schema.json",
  "providers": {
    "localai": {
      "baseUrl": "http://localhost:8080/v1",
      "api": "openai-completions",
      "apiKey": "sk-localai",
      "models": [
        { "id": "my-model", "contextWindow": 65536, "maxTokens": 8192 }
      ]
    }
  }
}
```

Run `pi`, open `/model` and select `my-model`. pi reloads the file when you open `/model`.
The `apiKey` value can also be `$LOCALAI_API_KEY` to read an environment variable.

Endpoint: `/v1/chat/completions`.

Caveat: if you leave out `contextWindow` and `maxTokens`, pi assumes 128000 and 16384.
Set them to match your model.

## Other agents

| Agent | Setting | Endpoint |
|---|---|---|
| Aider | `export OPENAI_API_BASE=http://localhost:8080/v1`, `export OPENAI_API_KEY=sk-localai`, then `aider --model openai/my-model` | Chat Completions |
| Continue | In `config.yaml`, add a model with `provider: openai`, `model: my-model`, `apiBase: http://localhost:8080/v1` and `apiKey: sk-localai` | Chat Completions |
| Cline | Choose the "OpenAI Compatible" provider. Set Base URL to `http://localhost:8080/v1`, add the key and the model ID. Set the context window under Model Configuration | Chat Completions |
| Goose | `export OPENAI_HOST=http://localhost:8080`, `export OPENAI_API_KEY=sk-localai`, `export GOOSE_PROVIDER=openai`, `export GOOSE_MODEL=my-model`. `OPENAI_BASE_PATH` defaults to `v1/chat/completions` | Chat Completions |

Goose documents that it relies heavily on tool calling, and that a model without tool
support can only chat.

## Troubleshooting

**401 Unauthorized.** LocalAI runs with `LOCALAI_API_KEY`, and the agent sent no key or
a wrong key. Check that the agent's key variable has the same value. For Claude Code, a
key in the wrong variable still reaches LocalAI, because it reads both the `Authorization`
and `x-api-key` headers. A proxy in front of LocalAI may drop those headers.

**404 model not found.** The model name in the agent does not match a name from
`GET /v1/models`. Fix the name in the agent, or add a
[model alias]({{% relref "features/model-aliases" %}}). For Claude Code, also set
`ANTHROPIC_DEFAULT_HAIKU_MODEL`.

**The agent chats but never calls tools.** The model has no tool support, or LocalAI
cannot parse its tool calls. Run the tool call test from the section
[Check that it works](#check-that-it-works). Use a model trained for tool calling, and
the model's own chat template.

**Errors about context length, or the agent forgets earlier steps.** The context window
is too small. Raise `context_size` in the model YAML or `--context-size`, restart the
model, and set the same value in the agent where it has a setting for it.

**Connection refused.** Check the address and the port. If the agent runs in a container
or on another machine, `localhost` is not your LocalAI server. Use the host address, and
make sure LocalAI listens on it (`--address`).

**Slow first answer.** LocalAI loads the model on the first request. The agent may time
out before the model is ready. Send one request with `curl` first.
