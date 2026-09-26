# Model failover chains

Date: 2026-09-26
Status: design approved in brainstorming, pending spec review

## Problem

A LocalAI instance that serves a model from a remote upstream (for example a
`cloud-proxy` model that points at a larger LocalAI cluster) has no way to fall
back to a local model when that upstream is unhealthy. Clients that want this
today build it themselves. The wingman voice assistant, for example, keeps an
ordered list of realtime WebSocket endpoints, quarantines an endpoint after
repeated backend errors, and reconnects to the next one. This has three costs:

- Every client reimplements failover, health tracking and recovery.
- The switch happens at the session level. A realtime conversation loses its
  context when the client reconnects to another endpoint.
- The client sees at least one failed request before it reacts.

## Goal

A model config can declare an ordered **failover chain** of target models.
LocalAI serves each request for the chain from the highest-priority healthy
target, retries on the next target when a target fails before the response is
committed, probes targets actively, fails back with hysteresis, and tells
clients when the active target changes.

Because a chain is a model name, it works in every place that takes a model
name, including the `llm`, `transcription`, `tts` and `vad` fields of a
realtime pipeline. The realtime session stays on the local instance, so a
switch changes the stage that serves the next turn and the conversation
history survives.

## Non-goals

- A dedicated WebUI chain editor and live health view. That is a follow-up
  (sub-project 3). This spec only registers the config fields in the field
  metadata registry, so the generic model editor can show them.
- The `localai-proxy` backend (a remote target kind that covers the full
  LocalAI API through gRPC). That is a follow-up (sub-project 1). This spec
  defines the "remote target" probe path it will plug into.
- Shared failover state across several LocalAI frontends. State is in memory
  and per instance.
- Transparent failover after a response has started to stream.

## Config schema

A chain is a model config with a `failover` block. Like an alias, it has no
backend of its own.

```yaml
name: assistant-llm
failover:
  targets:
    - model: argus-llm          # for example a cloud-proxy config
    - model: gemma-local
      warm: true                # load at startup, never evict
  probe:
    interval: 15s               # liveness probe interval
    timeout: 5s
  trip:
    errors: 1                   # retryable failures in `window` that mark a target down
    window: 30s
  recovery:
    probes: 3                   # consecutive inference probes to confirm recovery
    min_dwell: 60s              # minimum time on a lower target before fail-back
```

Only `targets` is required. The other values in the example are the defaults.

### Validation

The config loader rejects a chain when:

- `failover` is set together with `alias` or `backend`.
- `targets` has fewer than 2 entries.
- A target does not exist, or the same target is listed twice.
- A target is itself a chain. Chains do not nest.

A target can be an alias. The alias is resolved one hop, as it is today.

The loader logs a warning, and does not reject, when:

- The known usecases of the targets do not overlap (for example an LLM and a
  TTS model in one chain). Usecases are often inferred, so this cannot be an
  error.
- `warm: true` is set on a remote target. The flag has no effect there.

### Target kinds

The kind decides how a target is probed. It is inferred from the backend of the
target config:

- **remote**: `cloud-proxy`, and `localai-proxy` when it exists.
- **local**: every other backend.

### Naming in responses and accounting

The behaviour matches aliases. Responses echo the chain name. Usage and traces
record `requested=<chain>` and `served=<target>` through the existing
`ContextKeyRequestedModel` and `ContextKeyServedModel` keys.

## Failover manager

New package: `core/services/failover`. The application creates one `Manager` at
start and keeps it in sync with the model config loader. When a chain is added,
edited or removed (from YAML, the model editor API or the MCP tools), the
manager updates without a restart.

### State

- Health is tracked **per target**. A target that is in two chains is probed
  once, and a failure marks it down for both.
- The active target is tracked **per chain**.

Target states:

```
            trip (errors within window, from requests or probes)
 healthy ───────────────────────────────▶ down
    ▲                                       │ liveness probe passes
    │ recovery.probes consecutive           ▼
    └──── inference probes pass ◀──── recovering ──(any failure)──▶ down
```

- At startup, targets are `healthy`. The manager runs one liveness pass
  immediately, and in-request retry covers the gap until it completes.
- A target whose config is removed becomes `missing`. It is treated as `down`,
  and the chain reports it.

Chain states:

- `primary`: the active target is target 0.
- `fallback`: the active target is a lower target.
- `degraded`: all targets are down.

### Selecting the active target

- The active target is the highest-priority `healthy` target.
- Failover to a lower target is immediate.
- Fail-back to a higher target happens only when that target is `healthy`
  (which already needs `recovery.probes` passing inference probes) **and** the
  current target has been active for at least `min_dwell`.
- When the chain is `degraded`, requests still try every target in priority
  order. A probe can lag behind a recovery, so the manager does not fail fast.
- A manual pin (see API) forces the active target. While a pin is set, probes
  continue and report state, but they do not change the active target.

### Probes

| Target | Liveness (steady state) | Recovery confirmation |
|---|---|---|
| remote | `GET <upstream>/readyz`, and the upstream model is listed in `GET <upstream>/v1/models` | one minimal real request, chosen by usecase |
| local, `warm: true` | gRPC `HealthCheck` on the loaded backend. If the backend is not loaded (it crashed), a reload is the recovery attempt. | the same minimal request, run in-process |
| local, cold | the config and model files exist and the backend is installed. The model is never loaded only to probe it. | none. After a trip, the target returns to `healthy` when `min_dwell` has passed. The next real request is the test. |

Minimal requests by usecase:

- chat and completion: `max_tokens: 1`
- embeddings: the input `"ping"`
- transcription: 200 ms of silence
- TTS: the text `"ok"`
- expensive usecases (image, video, 3D): no inference probe. Liveness is the
  confirmation.

Probe load rules:

- Each chain has one ticker with jitter. A target shared by chains is probed
  once.
- A successful real request counts as a liveness pass, so a busy target is
  almost never probed.
- Inference probes run only while a target is `recovering`.
- Remote probes use the URL and API key from the target's proxy config.
- Probe results go into the same trip counter as request failures.

### Warm targets

The manager loads `warm: true` targets at startup and marks them pinned in the
watchdog, so LRU and idle eviction skip them. They still count toward the
active backend limit. When pinned warm targets leave no room for another load,
that load fails with an error that names them. The docs state this.

### Events

Each change of a target state or of an active target produces an event on an
internal bus:

```
{chain, target, from, to, state, reason, error, at}
```

`reason` is one of `trip`, `recovery`, `manual`, `degraded`, `missing`.

## Request path (HTTP)

### Resolution

In `core/http/middleware/request.go`, next to the alias block, a chain config is
resolved with `mgr.Plan(chain)`. The plan is the ordered list of attempts:

1. the active target,
2. the other `healthy` targets in priority order,
3. the `down` targets, only when the chain is `degraded`.

The middleware stores the plan in the request context and sets
`MODEL_CONFIG` to the config of the first target. Handlers do not change. The
plan is fixed when the request starts, so a config reload does not affect
requests that are in progress.

### In-request retry

A new middleware wraps the handler. It replaces the response writer with one
that records whether the response is committed:

- A response with status 500 or higher is buffered until the handler returns,
  as long as no body has been flushed. Error bodies are small, so the wrapper
  can discard them.
- A streaming response (SSE, or any flushed body) is committed at the first
  flush.

When the handler returns a retryable error and the response is not committed,
the wrapper calls `mgr.ReportFailure(target, err)`, sets the config of the next
target in the plan, and runs the handler again. When the handler succeeds, the
wrapper calls `mgr.ReportSuccess(target)`. When the response is committed and
then fails, the wrapper reports the failure and does not retry.

Request bodies:

- JSON bodies are already parsed into the request context.
- Multipart bodies are cached by `ParseMultipartForm`.
- Other bodies are buffered up to a limit. A larger body gets no in-request
  retry. The failure still counts toward the trip.

### Retryable errors

`failover.IsRetryable(err, status)` returns true for:

- connection and dial errors
- timeouts, when the client did not cancel the request
- gRPC `Unavailable`, `Internal`, `DeadlineExceeded` and `Unknown`
- upstream HTTP 5xx
- model load failures

It returns false for client cancellation, 4xx responses and validation errors.
These do not trip a target, because the next target would reject the same
request.

### Handler audit

Each endpoint family must be safe to run again before its response is
committed: chat, completions, embeddings, transcription, TTS, image generation,
rerank, VAD and sound detection. An endpoint that is not safe gets no
in-request retry (its failures still trip the target). The PR lists these
endpoints.

### Response headers

Every response for a chain carries:

- `X-LocalAI-Served-Model: <target>`
- `X-LocalAI-Failover: fallback` or `degraded`, when target 0 did not serve the
  request

Plain HTTP clients can see failover without subscribing to events.

## Request path (realtime)

- In `core/http/endpoints/openai/realtime_model.go`, a pipeline stage that names
  a chain is resolved **for each call** of `wrappedModel`, not once at session
  start. A helper, `mgr.Do(ctx, chain, func(cfg *config.ModelConfig) error)`,
  goes through the plan with the same classification as HTTP.
- Streaming stages (`Predict` with a token callback, `TTSStream`,
  `TranscribeStream`) wrap the callback. A retry is allowed only until the first
  token or audio chunk goes to the client. After that, the turn fails as it does
  today, the target is tripped, and the next turn uses the next target.
- `TranscribeLive` is resolved when it opens. A failure in the middle of the
  stream ends it in the existing way, and the next utterance opens it again on
  the new target.
- The conversation history is in the realtime session on this instance. A
  switch of the LLM stage keeps it.
- `Warmup` warms the active target of each chain stage.

## API and events

All endpoints use the global auth middleware. `GET` endpoints and the event
stream need standard auth. The pin endpoints are admin only.

### REST

`GET /api/failover` returns all chains:

```json
{"chains":[{"name":"assistant-llm","state":"fallback","active":"gemma-local",
  "active_since":"2026-09-26T10:00:00Z","pinned":null,
  "targets":[
    {"model":"argus-llm","kind":"remote","warm":false,"state":"recovering",
     "consecutive_ok":1,"last_probe":"2026-09-26T10:04:10Z",
     "last_error":"503 no healthy nodes"},
    {"model":"gemma-local","kind":"local","warm":true,"state":"healthy"}]}]}
```

`GET /api/failover/{chain}` returns one chain.

`POST /api/failover/{chain}/pin` with `{"target": "<model>"}` forces a target.
`DELETE /api/failover/{chain}/pin` removes the pin. The pin is in memory and a
restart clears it.

### Server-sent events

`GET /api/failover/events`:

- The first event is `snapshot`, with the same payload as `GET /api/failover`.
  A new client knows the current state without a race against a separate GET.
- Then `chain.switched` with `{chain, from, to, state, reason, at}`, and
  `target.state` with `{target, from, to, reason, error, at}`.
- A keepalive comment every 15 s.

### Realtime server event

`localai.model.failover`:

```json
{"type":"localai.model.failover","chain":"assistant-llm","stage":"llm",
 "from":"argus-llm","to":"gemma-local","state":"fallback","reason":"trip"}
```

- The server sends it to every session whose pipeline uses the chain when the
  chain switches.
- The server also sends it once for each chain stage when the session starts,
  with `reason: "initial"` and `from` empty. A client knows at the start whether
  it runs on the primary or on a fallback.
- `stage` is one of `llm`, `transcription`, `tts`, `vad`, `sound_detection`.

### Observability

- Metrics: `localai_failover_switches_total{chain,from,to,reason}` and
  `localai_failover_target_up{target}`.
- Each failed attempt in a request is recorded in the Traces UI, so a request
  served by target 2 shows why target 1 was skipped.

## Capability surfaces

As required by `.agents/api-endpoints-and-auth.md`:

- Handlers in `core/http/endpoints/localai/failover.go` with swagger blocks, tag
  `failover`. Routes in `core/http/routes/localai.go`. `make swagger`.
- An `instructionDefs` entry for the new tag in
  `core/http/endpoints/localai/api_instructions.go`, and the count in
  `api_instructions_test.go`.
- `failover.*` fields in the config field metadata registry
  (`core/config/meta/registry.go`), in a new `failover` section next to
  `alias`, so the generic model editor can show and edit them.
- MCP tools in `pkg/mcp/localaitools/`: `list_failover_chains`,
  `pin_failover_target` and `unpin_failover_target`, in the `inproc` and
  `httpapi` clients, the skill prompts, and `toolToHTTPRoute` in
  `coverage_test.go`. Chains are created and edited through the existing model
  config tools.
- A docs page, `docs/content/features/model-failover.md`, linked from
  `model-aliases.md`, `openai-realtime.md` and the cloud-proxy docs.

## Testing

Ginkgo and Gomega, like the rest of LocalAI. The coverage baseline must not go
down.

- **State machine**, with a fake clock: trip; recovery after N inference probes;
  `min_dwell` hysteresis; `degraded`; pin and unpin; a target shared by two
  chains; a `missing` target.
- **`IsRetryable`**: a table of error and status cases.
- **Config validation**: nested chain, `alias` with `failover`, fewer than 2
  targets, missing target, duplicate target, the usecase warning.
- **HTTP integration**: two fake OpenAI-compatible upstreams (`httptest`) behind
  `cloud-proxy` target configs.
  - Upstream 1 fails. The request is served by upstream 2 with no client error,
    `X-LocalAI-Served-Model` is set, and the SSE stream sends `chain.switched`.
  - Upstream 1 recovers. Fail-back happens only after N inference probes and
    `min_dwell`.
  - Upstream 1 fails after the first SSE chunk. There is no retry, the client
    gets the error, the target trips, and the next request goes to upstream 2.
- **Handler audit**: one retry test for each endpoint family that proves it is
  safe to run again before commit.
- **Realtime**: a pipeline whose LLM stage is a chain of fake backends. The test
  checks the `initial` event, the `localai.model.failover` event when the
  primary fails, and that the conversation history is kept after the switch.
- **API**: authenticated and unauthenticated access to every endpoint; pin
  requires admin.

## Follow-ups

1. `localai-proxy` backend: a fork of `cloud-proxy` that forwards every gRPC
   method (Predict, Embedding, AudioTranscription, TTS, GenerateImage, Rerank,
   VAD, sound detection) to the REST API of an upstream LocalAI. This lets a
   remote model serve any realtime pipeline stage.
2. WebUI: a chain editor and a live health view built on
   `GET /api/failover/events`.
3. Wingman: use one local LocalAI endpoint with chains for its pipeline stages,
   and react to `localai.model.failover` events instead of its own endpoint
   supervisor.
