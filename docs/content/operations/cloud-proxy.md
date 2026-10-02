+++
title = "Cloud passthrough proxy"
weight = 28
toc = true
url = "/features/cloud-proxy/"
description = "Forward requests to OpenAI, Anthropic, or any compatible provider"
tags = ["Proxy", "Cloud", "Routing", "Advanced"]
categories = ["Features"]
+++

![Cloud proxy: a local API call is proxied to a hosted model while PII is redacted out and back](/images/diagrams/cloud-proxy-sequence.png)

LocalAI can forward chat-completion and Anthropic Messages requests to an
external provider instead of running them through the local gRPC backend
pipeline. Configure a model with `backend: cloud-proxy` and a `proxy.upstream_url`,
and LocalAI bypasses templating, MCP injection, and the local model loader
entirely - the upstream sees the body the client sent (with only the top-level
`model` field optionally rewritten).

The streaming PII filter still runs over the upstream's SSE stream, so cloud
egress remains subject to the same redaction rules a local model would apply.

## When to use this

- Mix local and cloud models in the same LocalAI instance - clients hit one
  endpoint, LocalAI dispatches per model.
- Apply LocalAI's auth, usage tracking, and PII redaction to cloud traffic
  before the body leaves the network.
- Use the intelligent router to send small or simple prompts to a local model
  and complex ones to Claude or GPT-4o.

To fall back to a local model when the upstream is down, list the proxy model
in a [failover chain]({{%relref "features/model-failover" %}}).

## How it works

1. Request hits LocalAI on `/v1/chat/completions` (OpenAI-shaped) or
   `/v1/messages` (Anthropic-shaped).
2. The standard auth and routing middleware runs.
3. Per-model PII redaction runs request-side as it would for any model.
4. The handler detects the `cloud-proxy` backend in passthrough mode and
   loads the cloud-proxy gRPC backend, which owns the outbound HTTP.
5. The backend POSTs the body to `proxy.upstream_url` with provider-aware
   authentication, then streams the SSE response back to core.
6. The streaming PII filter rewrites per-token text in flight; the upstream's
   event names and metadata pass through unchanged.

Passthrough mode is **wire-format-faithful** - it does not translate request
shapes between providers. A client posting an OpenAI-shaped body to an
Anthropic upstream will get a confused upstream. Use the matching wire format,
or switch to translate mode (below).

## Configuration

The cloud-proxy backend has one knob - the provider it should authenticate
against - and two modes:

| `proxy.mode` | What it does | When to use |
|---|---|---|
| `passthrough` (default) | Forwards the request body verbatim to `upstream_url`. Client must speak the upstream's wire format. | Same wire format on both ends. |
| `translate` | Backend converts internal proto to the upstream's wire format. Client can speak OpenAI-shaped requests to an Anthropic upstream, etc. | Cross-format adaptation. |

`proxy.provider` selects the auth scheme and (in translate mode) the wire
format. Supported values: `openai`, `anthropic`.

If the upstream requires an API key, configure either an environment variable
(`api_key_env`) or a file (`api_key_file`). The key never appears in the config
file or the admin UI. If the upstream requires no API key, omit both fields.

### OpenAI passthrough

```yaml
name: gpt-4o-proxy
backend: cloud-proxy

# When set, replaces the client's "model" field before forwarding.
# Useful when the LocalAI alias differs from the upstream's canonical name.
proxy:
  mode: passthrough
  provider: openai
  upstream_url: https://api.openai.com/v1/chat/completions
  api_key_env: OPENAI_API_KEY
  upstream_model: gpt-4o
  request_timeout_seconds: 120

# PII filtering defaults to ON for cloud-proxy backends. Override by setting
# pii.enabled: false explicitly. Per-pattern action overrides go in
# pii.patterns; see the Middleware admin page or the Middleware feature doc.
pii:
  enabled: true
```

Then start LocalAI with the API key in the environment:

```bash
export OPENAI_API_KEY=sk-...
local-ai run
```

Clients hit `http://localhost:8080/v1/chat/completions` with `"model": "gpt-4o-proxy"`
and the request lands on OpenAI's API.

### Anthropic passthrough

```yaml
name: claude-sonnet-proxy
backend: cloud-proxy

proxy:
  mode: passthrough
  provider: anthropic
  upstream_url: https://api.anthropic.com/v1/messages
  api_key_env: ANTHROPIC_API_KEY
  upstream_model: claude-3-5-sonnet-20241022
  request_timeout_seconds: 300

pii:
  enabled: true
  # Block - not just mask - leaked credentials before they reach the upstream.
  patterns:
    - id: api_key_prefix
      action: block
```

Anthropic clients hit `http://localhost:8080/v1/messages` with
`"model": "claude-sonnet-proxy"`.

### Other OpenAI-compatible providers

Most third-party providers (Together, Groq, DeepInfra, OpenRouter, …) speak
the OpenAI chat-completions wire format. Use `provider: openai` with the
provider's URL and, if required, its API key:

```yaml
name: llama-3-70b-via-together
backend: cloud-proxy

proxy:
  mode: passthrough
  provider: openai
  upstream_url: https://api.together.xyz/v1/chat/completions
  api_key_env: TOGETHER_API_KEY
  upstream_model: meta-llama/Llama-3-70b-chat-hf
```

### Upstreams without an API key

For an OpenAI-compatible upstream that accepts requests without authentication,
omit both `api_key_env` and `api_key_file`:

```yaml
name: internal-chat-proxy
backend: cloud-proxy

proxy:
  mode: passthrough
  provider: openai
  upstream_url: http://inference.internal:8000/v1/chat/completions
  upstream_model: my-model
```

Replace the example URL and model name with your upstream's values. LocalAI
loads this configuration without resolving a key and adds no upstream
`Authorization` header. This also applies to OpenAI-compatible upstreams in
translate mode.

Omitting both fields differs from setting `api_key_env` to an empty or unset
environment variable: the latter causes a backend load error.

LocalAI's client authentication is separate. Clients must still authenticate
to LocalAI when its authentication is enabled. LocalAI does not forward their
`Authorization` header to the upstream.

An upstream without API keys can still require another authentication or
payment protocol. Omitting these fields does not implement that protocol.

### Translate mode

In translate mode the cloud-proxy backend converts LocalAI's internal proto
to the provider's wire format. This lets a client speak one shape (e.g.
OpenAI Chat Completions) against an upstream that expects another (e.g.
Anthropic Messages).

```yaml
name: claude-via-openai-clients
backend: cloud-proxy

proxy:
  mode: translate
  provider: anthropic
  upstream_url: https://api.anthropic.com/v1/messages
  api_key_env: ANTHROPIC_API_KEY
  upstream_model: claude-3-5-sonnet-20241022
```

Translate mode currently routes only pure-text completions - tool calls,
image blocks, and per-request usage tokens are dropped through the
internal `Predict()` signature. Use passthrough mode when your clients need
the upstream's full feature set.

In translate mode, an Anthropic response with `stop_reason: "refusal"` is returned to the client as an error instead of an empty successful reply, for both non-streaming and streaming requests. A streamed response may already have delivered partial content when the refusal arrives. Responses that end normally (`end_turn`) are unaffected, even when their content is empty.

#### Anthropic prompt caching

`proxy.cache_prompt: true` makes the translator add Anthropic
[prompt-cache](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)
breakpoints (`cache_control: {type: ephemeral}`) to the stable prefix of every
request: the system block, the last tool, and the final message block (at most
three of Anthropic's four allowed breakpoints). Anthropic then serves that
repeated prefix at the cache-read rate (~0.1x input) on subsequent calls, which
sharply cuts cost on agentic or multi-turn workloads that re-send a large,
unchanging system-plus-tools prefix each turn.

The flag only applies with `mode: translate` and `provider: anthropic`; it has
no effect in passthrough mode, for other providers, or when unset (the system
field is then still emitted as a bare string).

```yaml
name: claude-cached
backend: cloud-proxy

proxy:
  mode: translate
  provider: anthropic
  upstream_url: https://api.anthropic.com/v1/messages
  api_key_env: ANTHROPIC_API_KEY
  upstream_model: claude-3-5-sonnet-20241022
  cache_prompt: true
```

## Loading secrets from a file

`api_key_file` is an alternative to `api_key_env` when your secret manager
mounts keys as files (e.g. Kubernetes secrets, Docker secrets, Vault Agent):

```yaml
proxy:
  api_key_file: /run/secrets/openai_api_key
```

The file is read at backend load time and trimmed of surrounding whitespace.
`api_key_env` and `api_key_file` are mutually exclusive.

## Combining with the intelligent router

A router model can spread traffic across local and cloud candidates. The
score classifier reads the policy descriptions and routes per request:

```yaml
name: smart-router
router:
  classifier: score
  classifier_model: arch-router-1.5b
  fallback: qwen-3-7b-local
  activation_threshold: 0.40
  policies:
    - label: casual
      description: small talk, greetings, short answers
    - label: code
      description: writing or debugging code in any programming language
    - label: heavy-reasoning
      description: long-form analysis, complex math, multi-step reasoning
  candidates:
    - model: qwen-3-7b-local
      labels: [casual]
    - model: gpt-4o-proxy
      labels: [casual, code]
    - model: claude-sonnet-proxy
      labels: [casual, code, heavy-reasoning]
```

The router rewrites `input.Model` to the chosen candidate; per-model PII,
ACLs, and the cloud-proxy fork all run against the resolved target.

See [Middleware: PII filtering and intelligent routing]({{< relref "middleware.md" >}})
for the full router and PII-filter reference.

## Proxying to another LocalAI (`localai-proxy`)

`cloud-proxy` forwards chat and Messages requests only. To serve a model from
another LocalAI instance for every API it has, use `backend: localai-proxy`.
The backend receives the request from the local pipeline like any other
backend and sends it to the REST API of the upstream LocalAI. Because it is a
normal backend, a `localai-proxy` model can be a stage of a realtime pipeline
or a target of a [failover chain]({{% relref "features/model-failover" %}}).

```yaml
name: remote-llm
backend: localai-proxy
known_usecases: [chat]
proxy:
  # Base URL of the upstream LocalAI. Do not add /v1 or an endpoint path:
  # the backend adds the path for each API.
  upstream_url: https://argus.lan:8080
  # The model name on the upstream. When empty, the name of this config.
  upstream_model: gemma-3-12b
  # Optional. The upstream API key, from an environment variable
  # (or api_key_file). Sent as "Authorization: Bearer <key>".
  api_key_env: ARGUS_API_KEY
  # Optional. Time limit for each non-streaming request. Streams have no limit.
  request_timeout_seconds: 120
```

A model that does live transcription in a realtime pipeline also names a
realtime pipeline on the upstream. The backend opens a transcription session
on the upstream `/v1/realtime` endpoint with that pipeline:

```yaml
name: remote-stt
backend: localai-proxy
known_usecases: [transcript]
options:
  - realtime_pipeline:asr-pipeline
proxy:
  upstream_url: https://argus.lan:8080
  upstream_model: parakeet
```

Set `known_usecases` on every `localai-proxy` model. Failover uses it to match
targets, and LocalAI cannot guess the usecases of a remote model. For a chat
model, `known_usecases: [chat]` has one more effect: LocalAI sends the chat
messages to the upstream `/v1/chat/completions` endpoint, and the upstream
applies its own chat template, tool parsing and reasoning parsing. Without
`chat`, or when the config has its own templates, LocalAI renders the prompt
locally and sends it to `/v1/completions`. `proxy.mode` and `proxy.provider`
have no effect on this backend.

Supported APIs:

- Text: chat and completions (also streamed), embeddings, rerank, tokenize,
  detokenize, score.
- Audio: TTS (also streamed), sound generation, transcription (also streamed),
  live transcription (with `realtime_pipeline`), diarization, VAD, sound
  classification, audio transformations.
- Image, video and 3D: image generation, upscaling, video generation, 3D
  generation and animation. The backend downloads the files that the upstream
  generates.
- Vision: object detection, depth, face verification and analysis, voice
  verification, analysis and embeddings.
- Stores: set, get, delete, find.

Methods that have no REST API on the upstream return the gRPC error
`Unimplemented` ("localai-proxy: <method> has no upstream counterpart"). The
upstream returning `501 Not Implemented` maps to the same code. Both mean a
capability gap, not a broken target: audio encoding and decoding,
audio-to-audio streams, token classification (PII NER), model metadata,
fine-tuning, quantization and model export fall in this bucket. A failover
chain skips a target that returns `Unimplemented` and tries the next target,
but does not mark the target down. This applies to every API, also to the APIs
that report `Unimplemented` to the client as HTTP `501` (images, video, 3D,
detection, depth, face and voice).

Errors from the upstream: a 5xx response (other than 501) or a connection
failure becomes `Unavailable`, and a failover chain marks the target down. A
4xx response becomes `InvalidArgument`, and LocalAI returns it to the client
without a retry or a trip — except `429 Too Many Requests`, which becomes
`ResourceExhausted`: the request itself is fine, the upstream is just out of
capacity, so a failover chain retries it on the next target and trips the
rate-limited one, moving traffic off it until it recovers.

Known limits:

- Voice-profile paths pass through unresolved. When LocalAI resolves a TTS
  voice to a local file (for example a voice clone reference), the backend
  sends that path to the upstream, where it does not exist. Use voices that
  the upstream knows by name.
- Depth exports are not supported. The upstream writes them to its own disk,
  so a depth request with exports or a destination file returns
  `Unimplemented`. Depth maps and points without exports work.
- The REST transcription API has no end-of-utterance (`eou`) flag, so
  transcriptions through the proxy never set it. Live transcription through
  `realtime_pipeline` sets `eou` at the end of each utterance.
- Sound generation from a source audio file is not supported.
- Chat and completions do not forward grammars, so JSON mode and other
  grammar-constrained output are not enforced by the upstream. Images, audio
  and video attached to messages are not forwarded either. The backend logs a
  warning for each request that loses one of these fields.
- Streamed TTS cannot detect an upstream synthesis failure that ends the
  stream cleanly. The client receives the audio produced so far as a complete
  response, and a failover chain does not retry it. A stream that is cut off
  is reported as an error.
- `upstream_url` is the root of the upstream server. If it has a `/v1` path,
  the backend removes `/v1` and everything after it, and logs a warning.

## Limitations

- **Passthrough does no wire-shape translation.** Use `mode: translate` (with
  the constraints documented above) or send requests that match the upstream's
  format.
- **No output-side PII for non-streaming responses.** Streaming responses are
  filtered in flight; buffered responses pass through verbatim. Request-side
  PII covers both.
- **No retry or backoff.** Transient upstream failures bubble up to the client
  as `502 Bad Gateway`.
- **No request shape validation.** If the upstream rejects the body, its
  error envelope is forwarded to the client unchanged.

## Operational notes

- Cloud-proxy backends load like any other gRPC backend - they consume one
  process per loaded model and appear in the backend management view, but
  they hold no GPU memory.
- Usage stats and the trace log capture cloud-proxy requests like any other
  request. Token counts come from the upstream's `usage` field when present.
- Set `request_timeout_seconds` defensively - a hung upstream otherwise ties
  up an HTTP handler until the client disconnects.
