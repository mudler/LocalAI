# Failover chains: distributed mode, localai-proxy backend and WebUI

Date: 2026-09-26
Status: design approved in brainstorming, pending spec review
Builds on: `2026-09-26-model-failover-chains-design.md` (same PR)

## Problem

The failover chains in this PR work on one LocalAI instance. Three gaps
remain:

1. **Distributed mode.** With several frontends, chain definitions converge
   (config edits broadcast `cache.invalidate.models`), but runtime state does
   not. Each frontend probes, trips and pins on its own. A pin applies only
   on the frontend that received it and is lost on restart. `/api/failover`
   and its event stream show a different view on each frontend. `warm: true`
   pins only a frontend stub, so workers can evict the model, and every
   frontend preloads it.
2. **Remote targets cover only chat.** `cloud-proxy` forwards chat and
   completions. A remote LocalAI cannot serve transcription, TTS, VAD, sound
   detection or the other modalities as a chain target, so a realtime
   pipeline cannot fail over per stage between a remote and a local LocalAI.
3. **No UI.** Chains can be edited only as raw JSON in the model editor, and
   their health is visible only through the API.

LocalAI also has no rule that makes a feature state how it behaves with
several frontends. The failover feature shipped with per-instance state
because nothing asked the question.

## Goals

- **C. Distributed-aware failover.** Pins, target health and chain state are
  the same on every frontend. One frontend probes. Warm targets stay loaded on
  workers and are preloaded once.
- **A. `localai-proxy` backend.** A gRPC backend that serves every backend
  method with a REST counterpart by calling an upstream LocalAI, including
  live transcription through the upstream's realtime API.
- **B. WebUI.** A chain editor field, a chain template, a live health strip
  per chain, a "chain" badge in the model list, and a Failover overview page.
- **D. Contributor rule.** `AGENTS.md` and a new `.agents/distributed-state.md`
  require every stateful feature to choose and document a distributed mode.

## Non-goals

- Sharing failover state between instances that are not in one distributed
  cluster.
- A bridge for backend methods that have no REST or realtime counterpart
  upstream (audio encode/decode, metrics, status, fine-tune, quantization).
- Chains as router candidates or as the realtime classifier model.

---

## C. Distributed-aware failover

Standalone mode (no NATS, no PostgreSQL) keeps today's behaviour. Everything
below applies when distributed mode is on.

### Shared state

| State | Writers | Mechanism | Survives restart |
|---|---|---|---|
| Pins | any frontend (REST, MCP) | `syncstate.SyncedMap` named `failover.pins`, key = chain, with a gorm `Store` | yes |
| Target health: state, last error, since, consecutive passes | any frontend on a local transition, and the probe leader | `syncstate.SyncedMap` named `failover.targets`, key = target, NATS only | no |
| Chain state: active target, active since, chain state | the probe leader only | `syncstate.SyncedMap` named `failover.chains`, key = chain, NATS only | no |

- The pins table is created under `advisorylock.KeySchemaMigrate`, the same
  way the jobs store creates its tables.
- The manager gets a small `StateSync` dependency. The standalone
  implementation is a no-op; the distributed implementation wraps the three
  maps. The manager does not import NATS or gorm directly.
- A peer delta is applied through `OnApply`, which changes local state
  without publishing again (no echo loops).
- The NATS-only maps have no `Store`, so `Reconcile` would re-hydrate them
  empty. Instead the leader republishes every target and chain snapshot every
  10 s. A frontend that joins late converges within 10 s and uses its own
  state until then.

### Who does what

- **Every frontend** plans requests from the shared state. `Plan` already
  leaves unhealthy targets out of the attempt order, so a target tripped on
  another frontend is skipped at once. In-request retry stays local.
- **Any frontend** that sees a real request trip or pass a target publishes
  the new target state.
- **The probe leader** runs probes, recovery confirmation, dwell-based
  fail-back, the chain recompute and the warm preload. It publishes chain
  state. Leadership uses `advisorylock.RunLeaderLoop` with a new key
  `failover-prober` and the same 1 s interval as the scheduler.
- **Followers** do not recompute the active target. They adopt the leader's
  chain state. When no chain state has arrived yet (start-up), a follower
  uses its own recompute until the first delta.
- If the leader stops, another frontend takes the lock on its next tick.
  Pins and target health are not affected. Probes and fail-back pause for at
  most one tick.

### Events

Each frontend emits `chain.switched` and `target.state` to its own
subscribers (SSE, realtime `localai.model.failover`) when it applies a
change, whether the change is local or from a peer. Every frontend's stream
therefore shows the same events.

### Warm targets

- The SmartRouter's and ReplicaReconciler's pinned-model resolver includes
  `WarmTargets()`, so workers never evict a warm target.
- Only the leader preloads warm targets.
- A frontend's loaded check sees only its own model stubs. A warm target
  without a stub on the leader is treated as not loaded: its liveness passes
  and its recovery is inconclusive (it heals after `min_dwell`). Worker health
  is left to the node health monitor and to real requests.

### Tests

- Unit: two managers on the test fakebus (`core/services/testutil`). A pin on
  one shows on the other. A trip on one is skipped by the other's plan. Only
  the lock holder probes. A new leader resumes fail-back.
- A spec in `tests/e2e/distributed` when its harness supports two frontends
  cheaply; otherwise the unit specs are the coverage and the PR says so.

### Docs

`model-failover.md` replaces the "state is per instance" limit with a
"Distributed mode" section that describes the table above.

---

## A. `localai-proxy` backend

### Shape

- `backend/go/localai-proxy` is a separate OCI gallery backend, like
  `cloud-proxy`. It is registered in the `Makefile`, `backend/index.yaml` and
  `.github/backend-matrix.yml` (Linux amd64/arm64 and Darwin Metal), following
  `.agents/adding-backends.md`.
- It reuses cloud-proxy's auth header, HTTP client (no redirects) and
  hop-by-hop header helpers. It has no translate mode: the upstream is always
  LocalAI.
- `Load` refuses a model without proxy options, so greedy backend probing
  never selects it.

### Config

```yaml
name: argus-whisper
backend: localai-proxy
known_usecases: [transcript]
proxy:
  upstream_url: https://argus:8080   # base URL; each method appends its path
  upstream_model: whisper-large      # optional; default: this model's name
  api_key_env: ARGUS_KEY
  request_timeout_seconds: 60        # applies to non-streaming calls
```

- `core/backend/options.go` passes `ProxyOptions` to `localai-proxy` as well
  as `cloud-proxy`.
- `proxy.mode` and `proxy.provider` are ignored, with a load warning.
- A `localai-proxy` model without `known_usecases` loads with a warning, because
  usecases decide default-model selection and the failover inference probe.
- The failover prober already treats `localai-proxy` as remote.
  `UpstreamBase` accepts a base URL unchanged.

### Method mapping

| Backend method | Upstream endpoint |
|---|---|
| Predict, PredictStream | `/v1/chat/completions`, `/v1/completions` (SSE when streaming) |
| Embedding | `/v1/embeddings` |
| Rerank | `/v1/rerank` |
| TokenizeString, Detokenize | `/v1/tokenize`, `/v1/detokenize` |
| Score | `/api/score` |
| GenerateImage, UpscaleImage | `/v1/images/generations`, `/v1/images/upscale` |
| GenerateVideo | `/video` |
| Generate3D, Animate3D | `/3d/generations`, `/3d/animate` |
| TTS, TTSStream | `/tts` (streaming passes the upstream WAV header and PCM through) |
| SoundGeneration | `/v1/sound-generation` |
| AudioTranscription, AudioTranscriptionStream | `/v1/audio/transcriptions` (`stream=true` for SSE deltas) |
| AudioTranscriptionLive | upstream `/v1/realtime` transcription session (see below) |
| Diarize | `/v1/audio/diarization` |
| VAD | `/v1/vad` |
| SoundDetection | `/v1/audio/classification` |
| Detect, Depth | `/v1/detection`, `/v1/depth` |
| FaceVerify, FaceAnalyze | `/v1/face/verify`, `/v1/face/analyze` |
| VoiceVerify, VoiceAnalyze, VoiceEmbed | `/v1/voice/verify`, `/v1/voice/analyze`, `/v1/voice/embed` |
| Stores* | `/stores/set`, `/stores/get`, `/stores/delete`, `/stores/find` |
| AudioTransform | `/audio/transformations` |

Every request uses the upstream model name (`proxy.upstream_model`, else the
model name), the same derivation as `failover.UpstreamModel`.

Methods with no counterpart (AudioEncode, AudioDecode, AudioToAudioStream,
TokenClassify, GetMetrics, Status, ModelMetadata, fine-tune and quantization)
return gRPC `Unimplemented` with the message
`localai-proxy: <method> has no upstream counterpart`.

### Files

Core passes some inputs and outputs as local paths:

- Inputs (transcription and diarization audio, sound detection `src`, image
  `src` and reference images): the proxy reads the file and uploads it as
  multipart or base64, as the endpoint expects.
- Outputs (TTS, image, sound generation `dst`): the proxy writes the upstream
  result (bytes, or a download of the returned URL, or decoded base64) to
  `dst`.

### Live transcription bridge

The upstream realtime API needs a pipeline model (VAD and transcription). The
proxy takes it from the model's backend options:

```yaml
options:
  - realtime_pipeline:argus-transcribe   # an upstream pipeline config
```

Without this option, `AudioTranscriptionLive` returns the standard
"live transcription unsupported" error, and realtime uses its non-live
transcription path for the stage.

With the option, `AudioTranscriptionLive` opens a WebSocket to
`<upstream>/v1/realtime?model=<realtime_pipeline>`:

1. On the first `TranscriptLiveConfig`, send `session.update` with
   `type: transcription`, the input rate, the language and server VAD turn
   detection. Answer `ready` when `session.updated` arrives.
2. Forward each `TranscriptLiveAudio` as `input_audio_buffer.append`
   (PCM float to PCM16 base64 at the session rate).
3. Map `conversation.item.input_audio_transcription.delta` to `delta`, and
   `...completed` to `delta` (any remaining text) plus `eou: true`.
4. When the gRPC send side closes, commit the buffer, wait for the final
   completion, send `final_result` and close.
5. An upstream error or disconnect ends the gRPC stream with `Unavailable`.
   Word timings and `eob` are not available from the upstream and stay empty.

A realtime stage whose live session fails reopens on the next chain target at
the next utterance (behaviour from the base spec).

### Core changes

- **Rerank for Go backends.** `pkg/grpc` gets an optional rerank interface and
  a server handler, in the same way as `Score`.
- **`Unimplemented` is a capability gap.** The failover retry path (HTTP and
  `Manager.Do`) treats gRPC `Unimplemented` like an admission rejection: skip
  to the next target for this request, and do not trip the target. Otherwise a
  chain of a remote and a local target fails a request the local target can
  serve.

### Tests

- Unit: a fake LocalAI `httptest` upstream per method family (request path,
  body, model name, auth header, file upload and `dst` write).
- Unit: a fake WebSocket upstream for the live bridge (ready, deltas, eou,
  final result, upstream disconnect).
- E2E: `localai-proxy` models that point back at the test server's own mock
  models. A realtime pipeline whose stages are chains of a `localai-proxy`
  target and a local target completes a turn, and switches stage when the
  proxy target fails.

### Docs

A `localai-proxy` section in `docs/content/features/backends.md` (or the page
that documents `cloud-proxy`), and a remote-LocalAI example on
`model-failover.md`.

---

## B. WebUI

### Model editor

- A `failover-targets` field component replaces the JSON editor for
  `failover.targets` (`core/config/meta/registry.go` switches the component
  name). Each row has a model picker (`SearchableModelSelect`), move up/down,
  remove and a **warm** toggle. The toggle is disabled with a tooltip on remote
  targets. Inline validation: at least 2 targets, no duplicates, no chain as a
  target.
- The probe, trip and recovery fields stay in the Advanced group.
- A **Failover chain** template in `modelTemplates.js`, seeded with two empty
  targets. `?template=failover` preselects it.

### Health strip

When the edited model is a chain, a `FailoverChainStatus` component above the
form shows:

- the chain state (primary / fallback / degraded) and the active target, with
  the time since it became active;
- for each target: state, kind, warm, last probe and last error;
- **Pin** and **Unpin** for admins, behind a confirm dialog. The pinned target
  is marked.

### Model list and overview

- Installed models: a "chain" badge with the active target, in the same way
  as the alias badge.
- Operate → Runtime → **Failover**: a dense table with one row per chain
  (state, active target, target states, time since the last switch). Each row
  links to the chain in the model editor. With no chains, an empty state links
  to the Failover chain template.

### Live data

A `useFailoverChains` hook fetches `GET /api/failover`, then opens an
`EventSource` on `/api/failover/events`. `snapshot` replaces the state;
`chain.switched` and `target.state` patch it. The browser reconnects the
stream, and the hook polls every 15 s as a fallback (the Agent Status
pattern). A `failoverApi` group in `src/utils/api.js` holds the calls.

### Conventions

- Design tokens and CSS classes only; no new inline styles (inline-style
  ratchet).
- `StatusPill` tones: success for healthy and primary, warning for recovering
  and fallback, error for down and degraded, muted for missing.
- Strings in the `models` and `admin` i18n namespaces for all 8 locales.
- Pin controls are hidden when `useAuth().isAdmin` is false.

### Tests

Playwright specs with mocked APIs and a mocked `text/event-stream`: the editor
component, the template, the health strip updating on events, pin controls
for admins and not for other users, and the overview page. UI line coverage
stays at or above `core/http/react-ui/coverage-baseline.txt`.

---

## D. Contributor rule

- New guide `.agents/distributed-state.md`. A feature that keeps runtime
  state (in-memory maps, caches, pins, schedulers, background loops, probes)
  chooses one mode and documents it:
  - **shared**: `syncstate.SyncedMap`, with a `Store` when the state must
    survive a restart;
  - **single-runner**: an `advisorylock` leader loop;
  - **stateless per request**;
  - **per-instance**: allowed only with the reason written in the feature's
    docs.

  The guide gives one real example per mode (finetune jobs, the node health
  monitor, open responses, failover chains). Shared and single-runner
  features include a fakebus test with two instances.
- `AGENTS.md`: a Quick Reference bullet "Distributed-aware state" and a row in
  the Topics table.
- `.agents/api-endpoints-and-auth.md`: a checklist line "Stateful feature:
  distributed mode chosen and documented (see distributed-state.md)".

## Order of work

C first (it changes code already in the PR and the event contract the UI
reads), then A (it needs the `Unimplemented` classification and the rerank
handler), then B, then D. All in PR #12285.
