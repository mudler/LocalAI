
+++
disableToc = false
title = "Model Failover"
weight = 15
url = "/features/model-failover/"
+++

A **failover chain** is a model name that is served by an ordered list of
other models. LocalAI sends each request to the first healthy target. When a
target fails, the request moves to the next target, and later requests stay
there until the first target has recovered.

Use it to serve a model from a remote LocalAI or another OpenAI-compatible
provider, and to fall back to a local model when the remote one is down.

## Declaring a chain

```yaml
name: assistant-llm
failover:
  targets:
    - model: argus-llm          # for example a cloud-proxy model
    - model: gemma-local
      warm: true                # keep it loaded
```

Clients call `assistant-llm`. Each target is a normal model config. A chain
has no `backend` and no `parameters.model`.

Optional settings, with their defaults:

```yaml
failover:
  probe:
    interval: 15s     # how often an idle target is checked
    timeout: 5s
  trip:
    errors: 1         # failures within the window that mark a target down
    window: 30s
  recovery:
    probes: 3         # test requests a target must pass before it is used again
    min_dwell: 60s    # minimum time on a lower target before moving back
```

Rules:

- A chain needs at least 2 targets. A target can be an alias, but not another
  chain.
- A chain cannot also set `alias` or `backend`.
- Responses name the chain as the model. The `X-LocalAI-Served-Model` header
  names the target that served the request.

## How the target is chosen

- The active target is the first healthy target in the list.
- When a target fails, LocalAI marks it down and moves to the next target at
  once.
- LocalAI moves back to a higher target only when that target has passed
  `recovery.probes` test requests **and** the current target has been active
  for at least `recovery.min_dwell`. This stops an unstable upstream from
  moving traffic back and forth.
- When all targets are down, the chain is `degraded`. Each request still tries
  every target in order.

## Retry inside a request

When a target fails before the response starts, LocalAI sends the same
request to the next target. The client does not see the failure.

- LocalAI does not retry after the first byte of a response is sent (for
  example after the first streamed token). The request fails, the target is
  marked down, and the next request uses the next target.
- A target that is at its concurrency limit (an admission rejection) or that
  is disabled is skipped for that request without being marked down.
- LocalAI does not retry client errors (4xx), such as a prompt that is too
  long, because the next target would reject it too. A 4xx counts neither as
  a success nor as a failure for the target.
- Request bodies larger than 32 MiB are not retried.

When the primary did not serve the request, the response has the header
`X-LocalAI-Failover: fallback`, or `X-LocalAI-Failover: degraded` when all
targets were down.

## Health checks

| Target | Regular check | Check before moving back |
|---|---|---|
| Remote (`cloud-proxy`) | `GET /v1/models` on the upstream lists the model | one small real request, for example a 1-token completion |
| Local, `warm: true` | the backend answers a health check | one small real request |
| Local, not warm | none: judged only by real requests; it is never loaded only to check it | none: the target is used again after `min_dwell` |

A request that succeeds counts as a check, so a busy target is almost never
probed.

When a target is in more than one chain, its check settings come from the
first of those chains in name order.

## Warm targets

`warm: true` loads a local target at startup and protects it from idle and
LRU eviction, so a switch does not wait for the model to load. Warm targets
count toward the active backend limit (`--max-active-backends`) like any
pinned model: LocalAI never evicts them to make room, and if they fill the
limit, a new model still loads rather than being blocked.

## Realtime pipelines

A pipeline stage can name a chain:

```yaml
name: assistant
pipeline:
  vad: silero-vad
  transcription: whisper-chain
  llm: assistant-llm
  tts: voice-chain
```

LocalAI resolves the chain for every call of the stage. When a chain switches,
the session stays open and keeps its conversation. The next turn uses the new
target.

The session receives a `localai.model.failover` event for each chain stage when
it starts (`reason: initial`) and each time a chain switches:

```json
{"type":"localai.model.failover","chain":"assistant-llm","stage":"llm",
 "from":"argus-llm","to":"gemma-local","state":"fallback","reason":"trip"}
```

Limits:

- Chains are resolved only in full realtime pipelines. A transcription-only or
  sound-detection-only session does not resolve chains yet.
- After a `session.update` that changes the pipeline, `localai.model.failover`
  events keep describing the chains from session start.
- A chain used as a router candidate, or as the classifier-mode scoring model,
  is not resolved per call.

## Watching failover

- `GET /api/failover` lists every chain, its active target and the state of
  each target.
- `GET /api/failover/{chain}` returns one chain.
- `GET /api/failover/events` is a server-sent event stream. The first event is
  `snapshot` with the full state. Then `chain.switched` and `target.state`
  events follow.
- Metrics: `localai_failover_switches_total{chain,from,to,reason}` and
  `localai_failover_target_up{target}`.
- With tracing on, each skipped target appears in the Traces view with the
  error that made LocalAI skip it.

## Pinning a target

An admin can force a chain to one target, for example during maintenance:

```bash
curl -X POST http://localhost:8080/api/failover/assistant-llm/pin \
  -H 'Content-Type: application/json' -d '{"target":"gemma-local"}'
curl -X DELETE http://localhost:8080/api/failover/assistant-llm/pin
```

While a chain is pinned, only the pinned target serves it. Health checks
continue. A restart removes the pin.

## Assistant and MCP

The LocalAI Assistant and `local-ai mcp-server` offer `list_failover_chains`,
`pin_failover_target` and `unpin_failover_target`. Create and edit chains with
the model config tools, like any other model.

## Limits

- Failover state is kept in memory by each LocalAI instance. Several frontends
  in distributed mode each keep their own view.
- Chains do not nest.
- See also [model aliases]({{%relref "features/model-aliases" %}}) and the
  [realtime API]({{%relref "features/openai-realtime" %}}).
