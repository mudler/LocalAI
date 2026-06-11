+++
title = "Middleware: PII filtering and intelligent routing"
weight = 27
toc = true
description = "Per-model PII redaction and policy-based request routing"
tags = ["Routing", "Privacy", "PII", "Middleware", "Advanced"]
categories = ["Features"]
+++

![The request lifecycle: one shared hook chain for auth, model routing, and PII, with decision and event logs](/images/diagrams/middleware-lifecycle.png)

LocalAI ships a request-middleware layer that sits between the HTTP API and
the backend dispatcher. Two subsystems share that layer because they share
the same lifecycle hook: **PII filtering** scans the request body before it
reaches a backend, and the **intelligent router** rewrites `input.Model` so
a single client-facing model name fans out across multiple downstream
targets.

Both are inspected and configured from the same admin page
(`/app/middleware`), backed by the same REST surface (`/api/middleware/*`,
`/api/pii/*`, `/api/router/*`) and the same MCP tools.

## Request lifecycle

```
client ── auth ── route-model ── per-model PII ── backend ── client
                       │              │
                       │              └─── event log
                       └─── decision log
```

The router runs first (it picks the target model so per-model PII has
something to gate on), per-model PII runs next (gated by the resolved
config), and the backend executes. Filtering is **request-side only** —
the request body is scanned and rewritten before forwarding; the response
is not touched (NER over a streamed response is left as a follow-up). Each
subsystem writes to its own admin-visible log: `/api/router/decisions` for
routing, `/api/pii/events` for redaction and block actions.

---

## PII filtering

PII redaction is **NER-based and runs request-side (input)**. It is
**off by default**, flipping to **on for any `cloud-proxy` backend**
because that traffic crosses the network to a third-party provider. Pick a
[default detector](#instance-wide-defaults) so those models are actually
scanned. Explicit `pii.enabled` in a model's YAML always wins over the
backend default.

Filtering runs on every text-accepting endpoint that has an adapter wired:
`/v1/chat/completions` and `/v1/messages` (chat), `/v1/completions`,
`/v1/embeddings`, `/v1/edits`, and the Ollama `/api/chat`, `/api/generate`
and `/api/embed` endpoints, plus the [MITM proxy]({{< relref "mitm-proxy.md" >}})
request body. Image, audio (TTS/STT), video, rerank, and the realtime
WebSocket are not filtered yet (different prompt-PII semantics; realtime is
not HTTP middleware).

> The earlier regex pattern tier (`pii.patterns`, the built-in pattern
> catalogue, `--pii-config`, the `/api/pii/patterns|test|decide` endpoints)
> and response/streaming-side redaction have been **removed**. Detection is
> now driven entirely by token-classification (NER) models. Legacy keys
> no-op with a startup warning.

### Detector models

A **detector** is a `token_classify` model (e.g. an `openai-privacy-filter`
GGUF) that carries the detection *policy* in a top-level `pii_detection:`
block — defined once, on the model itself:

```yaml
name: privacy-filter-multilingual
backend: llama-cpp
embeddings: true              # TOKEN_CLS pooling
known_usecases:
  - token_classify
pii_detection:
  min_score: 0.5              # drop detections below this confidence
  default_action: mask        # applied to any detected group with no entry
  entity_actions:             # which PII to block vs mask vs allow-log
    PASSWORD: block
    CREDITCARD: block
    EMAIL: mask
```

`mask` rewrites the matched span to `[REDACTED:ner:<GROUP>]` in the request
body before forwarding. `block` returns HTTP 400 (`error.type=pii_blocked`)
without forwarding. `allow` detects and logs (a PIIEvent is still recorded)
but leaves the text unchanged. The entity-group names are whatever the model
emits (the privacy-filter family uses uppercase names like `EMAIL`,
`PASSWORD`, `CREDITCARD`).

### Pattern detector tier

NER is the wrong tool for high-entropy, highly-regular **secrets** — API keys,
tokens, private-key blocks. A trained NER model has no "API key" class, so it
fragments a key into the nearest categories it *does* know and can leave the
secret part exposed. Those secrets are exactly what a regex catches cheaply.

A **pattern detector** is a detector model (`backend: pattern`) that matches
secrets with a **restricted regex subset** compiled to Go's RE2 engine —
linear-time, no backtracking, no ReDoS. It runs entirely in-process: no model
download, no backend, zero VRAM. Install the gallery's **`secret-filter`** for a
ready-made set, or define your own:

```yaml
name: secret-filter
backend: pattern
known_usecases: [token_classify]        # so it appears in the detector picker
pii_detection:
  default_action: block                 # a leaked credential shouldn't leave
  builtins:                             # built-in catalogue (enable by name)
    - anthropic_api_key
    - openai_api_key
    - github_token
    - aws_access_key
    - private_key_block
  patterns:                             # operator-defined, restricted subset
    - name: INTERNAL_TOKEN
      match: "tok-[A-Za-z0-9]{32,64}"
      action: block                      # optional per-pattern override
      min_len: 36                        # optional length floor
```

A match is reported under its group (built-in group name, or the pattern
`name`), so `entity_actions` / `default_action` apply exactly as for NER.

**The restricted grammar** (validated at load — an invalid pattern is rejected,
not silently ignored):
- Allowed: literals, character classes `[…]` and `\w \d \s`, alternation,
  anchors `^ $ \b`, and quantifiers `? * + {m,n}`.
- Rejected: `.` (any-char), capturing groups, and `{n,m}` bounds over 4096.
- **Required anchor**: every pattern must contain a fixed literal run of at
  least 3 characters (e.g. `sk-ant-`, `ghp_`, `AKIA`). This admits real key
  shapes but rejects open-ended ones — an email or a bare `\w+` has no such
  anchor and belongs to the [NER tier](#detector-models).

Use both tiers together: reference an NER detector *and* a pattern detector in a
model's `pii.detectors` (or as instance defaults); their hits union, and a
`block` from either rejects the request.

### Consuming models

Any model opts in by enabling PII and referencing one or more detectors —
no per-consumer policy:

```yaml
name: claude-strict
backend: cloud-proxy
proxy:
  mode: passthrough
  provider: anthropic
  upstream_url: https://api.anthropic.com/v1/messages
  api_key_env: ANTHROPIC_API_KEY
pii:
  enabled: true               # default-on for cloud-proxy; explicit for audit
  detectors:
    - privacy-filter-multilingual
```

Multiple detectors **union** their detections; overlapping spans resolve to
the strongest action (`block` > `mask` > `allow`). A configured detector
that can't be loaded **fails the request closed** (HTTP 503,
`error.type=pii_ner_unavailable`) rather than silently skipping the check.
The same NER path runs on the [MITM proxy]({{< relref "mitm-proxy.md" >}})
request body for intercepted hosts. Response/output redaction is out of
scope for now.

### Instance-wide default detector

The **Detector models** table on the Middleware → Filtering page lists every
`token_classify` detector model (neural NER models and in-process pattern
matchers alike) and exposes a per-row **Default** toggle. Toggling a detector
on adds it to the instance-wide default detector set — one or more models
applied to any PII-enabled model that names none of its own `pii.detectors`.
It is persisted through `POST /api/settings` and read live, so a change takes
effect on the next request without a restart. A default that names a model no
longer loaded still appears (marked *not loaded*) so it can be toggled off.

This is what makes `cloud-proxy` / MITM redaction work out of the box: those
backends default to PII-enabled but ship no detector list, so without a
default detector the filter runs with nothing to scan. Set one here and
cloud-proxy traffic is scanned with no per-model config.

Resolution precedence (the single decision point is `ResolvePIIPolicy`,
shared by the chat middleware and the MITM listener so both agree):

1. An explicit `pii.enabled` on the model wins — `true` or `false`.
2. Otherwise PII is on if the backend defaults it on (`cloud-proxy`).
3. Detectors are the model's own `pii.detectors`; if it lists none, the
   instance-wide default detector(s) are used.

A model that resolves enabled but ends up with no detector at all (a
cloud-proxy model with no model detectors and no instance default) scans
nothing — set a default detector to close that gap.

### Admin page

The `/app/middleware` page (admin role only) has four tabs — **Filtering**,
**Routing**, **MITM Proxy** (see the [MITM doc]({{< relref "mitm-proxy.md" >}})),
and **Events**. The Filtering tab has a **Detector models** table (every
`token_classify` filter model, with the per-row Default toggle above and an
edit link to each detector's config, plus an *Add detector model* button) and
a per-model table listing only the models PII can actually apply to — chat /
completion / embeddings / edit consumers and cloud-proxy models, not
VAD/STT/image models or the detector models themselves. Each row reports the
**effective** `enabled` state as an inline **toggle** — flipping it writes an
explicit `pii.enabled` to that model's YAML (a server-side deep-merge that
preserves `pii.detectors` and every other field), so a cloud-proxy model shown
on by backend default can be turned off, and vice-versa — plus the
resolved detector(s) — with a *(default)* marker when they come from the
instance-wide default rather than the model's YAML — why it is on (`YAML` /
`backend default`), and the recent event count. Detection *policy*
(entity→action, min score) is still edited on each detector model's config
(Models → edit → PII), not globally.

### Analyze / redact API

The same detection pipeline is also exposed as a standalone service, so a
client can scan or sanitise a string **without** routing a full chat request
through it (the inline path above). Two endpoints, both requiring a normal API
key (the `pii_filter` feature — not admin):

- `POST /api/pii/analyze` — detect only. Returns the matched entity spans
  (`entity_type`, `source` `ner`|`pattern`, `start`/`end`, `score`, `action`)
  and a `blocked` flag, **without modifying the text**.
- `POST /api/pii/redact` — apply the configured policy. Returns `redacted_text`
  (with masked spans replaced by `[REDACTED:<id>]`) and `masked`; when a `block`
  action fires it returns `400` with `type: pii_blocked` and the offending
  entities — never a redacted body.

Both take the same request: `text` plus a detector selection — either explicit
detector model names in `detectors`, or a consuming `model` whose **effective**
policy is used: the model's own `pii.detectors`, else the
[instance-wide default detectors](#instance-wide-default-detector), exactly as
the inline filter resolves them. A `model` with PII disabled — or enabled but
with no detector anywhere — is a `400`: the inline filter would scan nothing
for it, and the API says so rather than implying a clean scan. The detection
policy lives on the detector models exactly as for the inline filter. The raw
matched value is never returned (an admin may pass `reveal: true` to include
the audit `hash_prefix`).

```bash
# Redact with an explicit pattern/NER detector
curl -sX POST http://localhost:8080/api/pii/redact \
  -H 'Authorization: Bearer $API_KEY' -H 'Content-Type: application/json' \
  -d '{"text":"reach me at jane@acme.io","detectors":["my-ner-model"]}'
# => {"redacted_text":"reach me at [REDACTED:ner:EMAIL]","masked":true,...}

# Analyze using a consuming model's configured detectors
curl -sX POST http://localhost:8080/api/pii/analyze \
  -H 'Authorization: Bearer $API_KEY' -H 'Content-Type: application/json' \
  -d '{"text":"sk-ant-api03-…","model":"gpt-4"}'
# => {"entities":[{"entity_type":"ANTHROPIC_KEY","source":"pattern",...,"action":"block"}],"blocked":true}
```

Calls are audited in the same event log, tagged with an `origin` of
`pii_analyze` / `pii_redact` (the inline filter records `middleware`, the MITM
proxy records `proxy`), so `GET /api/pii/events?origin=pii_redact` shows just
the redact-API rows.

### REST surface

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/api/pii/analyze` | api key (`pii_filter`) | Detect PII in a string; returns entity spans, no mutation. |
| POST | `/api/pii/redact` | api key (`pii_filter`) | Redact a string per policy; returns `redacted_text` or `400 pii_blocked`. |
| GET | `/api/pii/events` | admin | Recent middleware events — PII redactions, MITM connect/traffic, admission denials. Filterable by `correlation_id`, `user_id`, `pattern_id` (e.g. `ner:EMAIL`), `kind`, `origin`. |
| GET | `/api/middleware/status` | admin | Aggregated dashboard data: per-model PII state + detectors + router status + MITM status + admission status. One round-trip for the UI. |

### MCP tools

The same surface is mirrored through the LocalAI Assistant MCP server:

| Tool | Read/Write | Purpose |
|---|---|---|
| `get_pii_events` | read | Recent redaction / block events with optional filters. |
| `get_middleware_status` | read | Aggregator — the same payload as `GET /api/middleware/status`. |

Detection policy is part of a detector model's config, so it is managed
through the model-config tools (`edit_model_config`), not a dedicated PII
tool.

---

## Intelligent routing

A **router model** is a model whose YAML carries a `router:` block. When
a client addresses it (`"model": "smart-router"`), the middleware
classifies the prompt, picks a downstream candidate model, rewrites
`input.Model` to the candidate, and the standard model-resolution path
runs against that resolved target. ACL checks, disabled-state, and
per-model PII all apply to the resolved model — the router does
*model selection only*.

#### Depth-1 invariant

Candidates **must not** themselves be router models. A
`smart-router → claude-strict → cloud-proxy` chain is fine
(`claude-strict` is a regular cloud-proxy model). A
`smart-router → other-router → real-model` chain is rejected at runtime
by the middleware (the dispatcher returns HTTP 500 with a
`depth-1 invariant` error). This keeps the dispatch graph acyclic and
predictable.

#### Fallback

If no candidate's label set covers the active label set from the classifier,
or the classifier errors out, the router uses `cfg.Router.Fallback`.
An empty `fallback` causes the dispatch to fail with HTTP 500 rather
than silently routing somewhere unintended — fail-fast, not
silent-bypass.

### Available classifiers

LocalAI ships two classifier implementations. Pick one with `classifier:`
in the router YAML:

| Classifier | When to use | Underlying primitive |
|---|---|---|
| `score` (default) | Small classifier-tuned LM (Arch-Router-style). Best when label vocabulary is well-covered by next-token continuation. | `Score` gRPC primitive (llama-cpp, vLLM). |
| `colbert` | When label descriptions are abstract or short and a next-token classifier produces flat distributions. Robust on long-form policy descriptions. | rerankers backend in ColBERT mode (e.g. `bge-m3-colbert` from the gallery). |

Both classifiers share the same YAML shape: `classifier_model`,
`policies`, `candidates`, `fallback`, `activation_threshold`,
`classifier_cache_size`, and the optional `embedding_cache` block.

### The Score classifier

The `score` classifier works like this:

1. Build a Qwen/ChatML system prompt that lists every policy label with
   its description and primes the model to emit a label as the assistant
   turn.
2. Ask the classifier model to **score every policy label** as the
   first-token(s) continuation. This uses the `Score` gRPC primitive
   (`backend.proto::Score`), which returns per-candidate log-probabilities
   length-normalized so candidates of unequal token length stay
   comparable.
3. Softmax the length-normalized log-probabilities into a probability
   distribution over labels.
4. Threshold the distribution: every label whose probability passes
   `activation_threshold` joins the **active label set**.
5. Pick the FIRST candidate whose `Labels` is a superset of the active
   set. Admins order candidates smallest → largest so a single-label
   query routes to the smallest capable model, while a query that
   activates multiple labels falls to a candidate that covers them all.

This is the Arch-Router approach extended for multi-label. The
distribution carries more signal than the argmax — reading off the
spread lets one prompt activate multiple policies and route to a model
capable of all of them.

#### Recommended classifier model

[Arch-Router-1.5B](https://huggingface.co/katanemo/Arch-Router-1.5B) is
the canonical choice. It's a Qwen-2.5-1.5B-Instruct base trained
specifically on routing-policy continuation, so the ChatML system-prompt
+ label-continuation pattern produces well-separated label probabilities
without prompt tuning. The Q4_K_M GGUF runs on CPU, GPU, and Intel SYCL.

The classifier model must support the `Score` gRPC primitive (today: the
llama-cpp and vLLM backends) and use the ChatML chat template. Any small
ChatML instruct model works under those constraints, but expect flatter
probability distributions which translate to a higher
`activation_threshold` to keep noise out of the active label set.

On llama-cpp, scoring rides the server's task queue alongside
generation and embeddings, so the classifier may share a model config
with `chat`/`completion`/`embeddings` — a dedicated scorer model is no
longer required. Repeated calls with the same prompt also reuse the
prompt's KV cache across candidates.

### The Colbert classifier

The `colbert` classifier reranks each policy *description* against the
prompt via the rerankers backend and activates the labels whose
relevance scores clear `activation_threshold` (default 0.5 for
reranker-style scores in [0, 1]).

```yaml
router:
  classifier: colbert
  classifier_model: bge-m3-colbert  # gallery entry; loads BAAI/bge-m3 in ColBERT mode
  activation_threshold: 0.5
  policies:
    - label: code-generation
      description: writing, debugging, reading, or explaining code
    - label: casual-chat
      description: small talk, greetings, jokes
  candidates: [...]
```

The reranker scores the *description* (natural English) rather than
asking a small LM to score the *label* as a next-token continuation,
so it tends to be more robust when policy labels are abstract slugs
(`compliance-review`, `tier-2-support`). The trade-off is one
reranker round-trip per request — bge-m3 in ColBERT mode is fast
enough on GPU that this is comparable to the Score path for most
workloads. The `embedding_cache` block applies identically.

The reranker model's `type:` (in the model YAML) selects which
underlying scoring head loads — `colbert` for late-interaction MaxSim,
`cross-encoder` for cross-attention scoring. The classifier itself is
indifferent; pick the head that fits your latency / quality budget.

### YAML reference

```yaml
name: smart-router
known_usecases:
  - chat
router:
  # `score` (Arch-Router-style next-token scoring) or `colbert`
  # (rerank policy descriptions). See "Available classifiers" above.
  classifier: score

  # A model loaded by LocalAI that supports the Score gRPC primitive
  # (llama-cpp and vLLM ship implementations). Arch-Router-1.5B is the
  # canonical choice.
  classifier_model: arch-router-1.5b

  # Bounded LRU keyed on (case-folded, whitespace-trimmed) prompt — prompts
  # repeat in agent loops; the cache amortises the classifier round-trip
  # across them. 0 here means "use the default" (1024); the cache cannot be
  # disabled from YAML today.
  classifier_cache_size: 256

  # Softmax probability floor a label must clear to join the active label set.
  # 0 = use the package default (0.15). 0.40 is a better empirical
  # starting point on Arch-Router-1.5B — see the tuning note below.
  activation_threshold: 0.40

  # Used when no candidate covers the active label set, or the classifier
  # itself errors. Empty here = fail-fast with HTTP 500.
  fallback: qwen3-0.6b

  # The label vocabulary. Descriptions are fed verbatim into the
  # classifier's system prompt — short, action-oriented sentences work
  # best ("writing or debugging code", "small talk").
  policies:
    - label: code-generation
      description: writing, debugging, reading, or explaining code in any programming language
    - label: casual-chat
      description: small talk, greetings, jokes, or general conversation with no specific task
    - label: math-reasoning
      description: arithmetic, equations, percentage calculations, or step-by-step word problems

  # Routing table — order matters (smallest → largest). See "Score
  # classifier" above for the matching rule.
  candidates:
    - model: qwen3-0.6b
      labels: [casual-chat]
    - model: qwen_qwen3.5-2b
      labels: [code-generation, casual-chat, math-reasoning]
```

### Tuning `activation_threshold`

The threshold is the single knob you'll want to tune per
(classifier-model, policy-set) pair. On Arch-Router-1.5B with the
three-policy setup above, sweeping the threshold over a hand-labeled
30-prompt corpus produced:

| Threshold | Label-set accuracy | End-to-end routing accuracy |
|---:|---:|---:|
| 0.15 (package default) | 30% | 73% |
| 0.30 | 57% | 87% |
| **0.40** | **60%** | **90%** |
| 0.45 | 67% | 97% |
| 0.50 | 67% | 97% |

The classifier's argmax matches the dominant label 93% of the time on
this corpus — what the threshold controls is how much secondary-label
noise leaks into the active label set. Low thresholds push single-label
queries to multi-label-capable (larger) candidates unnecessarily; 0.40
keeps the dominant label dominant without losing genuine compound
activations.

Re-tune per (classifier-model, policy-set) pair. The `/api/score`
endpoint (see below) is the convenient probe — it returns the raw
length-normalized log-probabilities so you can sweep thresholds offline
without driving real chat completions.

### Embedding cache (L2)

Classification is the most expensive thing the middleware does. The
score classifier already memo-caches verbatim repeats (case- and
whitespace-folded prompt → decision); the **embedding cache** is the
L2 tier that catches *semantically similar* prompts — "How do I exit
vim?" and "i need to quit vim" can share a decision instead of running
the classifier twice.

Pairs naturally with a larger / slower classifier model: the steady-state
cost on cache hits collapses to one embedding round-trip plus a KNN
search, both well under 100ms with `nomic-embed-text-v1.5` + local-store.

#### Configuration

Add an `embedding_cache:` block to a router model:

```yaml
router:
  classifier: score
  classifier_model: arch-router-1.5b
  policies: [...]
  candidates: [...]

  embedding_cache:
    embedding_model: nomic-embed-text-v1.5    # any loaded embedding model
    similarity_threshold: 0.80                # cosine sim floor for a hit (default 0.80)
    confidence_threshold: 0.60                # min top-label prob to cache a decision (default 0.60)
    # store_name: router-cache-smart-router   # optional override; defaults to "router-cache-<router>"
```

Omit the block entirely to disable. The cache adds two new failure modes
(embedder unavailable, store unavailable) — both fall through to the
inner classifier so routing keeps working.

#### How it works

For each request:

1. Embed the probe prompt via the configured `embedding_model`.
2. KNN top-1 against the per-router local-store collection.
3. If similarity ≥ `similarity_threshold`, return the cached decision
   (`Cached=true`, `CacheSimilarity=<sim>` in the decision log).
4. Miss → run the inner classifier. If `decision.score >= confidence_threshold`,
   insert `(embedding, decision)` into the store. Low-confidence
   decisions are deliberately skipped so they can't poison future
   paraphrases.

The local-store collection is named `router-cache-<router-model-name>` by
default — each router gets its own collection so two routers can't
cross-contaminate. Collections persist on disk (local-store is the
canonical persistent vector backend), so the cache survives restarts.

#### Tuning notes

- **Similarity threshold**: 0.80 is the package default — re-tune
  per (embedding model, corpus). The histogram on the Routing tab
  shows where the cosine distribution actually sits; pick a
  threshold above the cross-intent cluster and below the paraphrase
  cluster.
- **Confidence threshold**: 0.60 corresponds roughly to "the
  classifier is committed to a top label." Don't lower this — caching
  unsure decisions propagates the uncertainty.
- **Cache flush**: invalidates automatically when the router YAML
  changes (the classifier cache is fingerprinted by `yaml.Marshal`),
  but the underlying local-store collection still holds the old
  payloads. Manual flush via local-store admin or by renaming
  `store_name` if you need a hard reset.
- **Latency budget**: an embedding round-trip (typically 30–80ms for
  small embedding models) plus KNN search (~5ms) is added to every
  *miss* on top of the classifier latency. Cache hits skip the
  classifier entirely. Break-even is around 7–10% hit rate; agent
  loops with repeated phrasing easily exceed this.

### Admin page

The `/app/middleware` page has a **Routing** tab listing every router
model's classifier, policies, candidates, and fallback. The **Events**
tab shows the decision log — one row per classified request with
correlation ID, requested model, served model, classifier name, active
labels, top-label score, and latency.

Routing decisions are stored in an in-process ring buffer (default
capacity 5,000). The decision log is for audit and tuning — the
canonical usage log lives in `/api/usage` and correlates by request ID.

### REST surface

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/router/status` | any | Router configuration: each router model's classifier, policies, candidates. |
| GET | `/api/router/decisions` | admin | Decision log with optional filters (`correlation_id`, `user_id`, `router_model`, `limit`). |
| POST | `/api/score` | admin | Direct access to the `Score` gRPC primitive — useful for offline threshold tuning. Body: `{"model": "<classifier-model>", "prompt": "<chatml-prompt>", "candidates": ["label-a", ...], "length_normalize": true}`. The llama-cpp and vLLM backends implement Score; other backends return `UNIMPLEMENTED`. |

### MCP tools

| Tool | Read/Write | Purpose |
|---|---|---|
| `get_router_decisions` | read | Recent decision log with optional filters. |
| `get_middleware_status` | read | Includes the router section listing configured router models. |

Mutating routing config — adding a candidate, changing the classifier
model — is YAML-only today; reload with `POST /models/reload` to pick
up edits without restarting.

### Operational notes

- **Reload after YAML edits.** The router configs are loaded at startup
  and cached. `POST /models/reload` re-reads from disk; the next request
  rebuilds the classifier from the new config (the classifier cache is
  fingerprinted by `yaml.Marshal(RouterConfig)` so it invalidates
  automatically).
- **Classifier latency** on Arch-Router-1.5B Q4_K_M is ~500ms steady
  for 3 policies on Intel SYCL. The score primitive re-decodes the full
  prompt for every candidate today (the KV cache is cleared between
  candidates); the prompt-KV-sharing optimization is on the perf TODO
  list in `backend/cpp/llama-cpp/grpc-server.cpp::Score`. Until then,
  `classifier_cache_size` is the highest-leverage knob for repeat-query
  workloads (agent loops).
- **Decision log size**: 5,000-entry ring buffer per process. The
  log is in-process and not persisted — pair with the usage log for
  long-horizon audit.

---

## Related features

- [Cloud passthrough proxy]({{< relref "cloud-proxy.md" >}}) — combine
  the router with `proxy-*` backends to send simple prompts to local
  models and complex ones to cloud providers.
- [MITM proxy]({{< relref "mitm-proxy.md" >}}) — apply the same PII
  filter to Claude Code, Codex CLI, and any HTTPS client without
  LocalAI holding their API keys.
- [Authentication]({{< relref "authentication.md" >}}) — admin role is
  required for mutating endpoints and the `/app/middleware` page; in
  no-auth single-user mode the synthetic local user has admin role
  automatically.
