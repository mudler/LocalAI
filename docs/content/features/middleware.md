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
reaches a backend (and the SSE stream on the way out), and the **intelligent
router** rewrites `input.Model` so a single client-facing model name fans
out across multiple downstream targets.

Both are inspected and configured from the same admin page
(`/app/middleware`), backed by the same REST surface (`/api/middleware/*`,
`/api/pii/*`, `/api/router/*`) and the same MCP tools.

## Request lifecycle

```
client ── auth ── route-model ── per-model PII ── backend ── streaming PII ── client
                       │                              │
                       └─── decision log              └─── event log
```

The router runs first (it picks the target model so per-model PII has
something to gate on), per-model PII runs next (gated by the resolved
config), the backend executes, and the streaming PII filter rewrites the
SSE response in flight. Each subsystem writes to its own admin-visible
log: `/api/router/decisions` for routing, `/api/pii/events` for redaction
and block actions.

---

## PII filtering

PII redaction is **per-model and off by default**. The default flips to
**on for any backend whose name starts with `proxy-`** because that traffic
crosses the network to a third-party provider. Explicit `pii.enabled`
in a model's YAML always wins over the backend default.

### Pattern catalog

The built-in regex tier ships six patterns. Each has a default action
(`mask`, `block`, or `route_local`) and a length cap that prevents
pathological inputs from blowing up scanning time:

| ID | Description | Default action | Max length |
|---|---|---|---|
| `email` | Email address | `mask` | 254 |
| `phone` | Phone number (international or US) | `mask` | 24 |
| `ssn` | US Social Security Number | `mask` | 11 |
| `credit_card` | Credit card number (Luhn-verified) | `mask` | 19 |
| `ipv4` | IPv4 address | `mask` | 15 |
| `api_key_prefix` | `sk-`, `pk-`, `xoxb-`, `ghp_`, `github_pat_` | **`block`** | 200 |

`mask` rewrites the match to `[REDACTED:<id>]` in the request body before
forwarding. `block` returns HTTP 400 with `error.type=pii_blocked` to the
client without forwarding. `route_local` is reserved for the routing
integration (see below) and falls back to `mask` when no local route is
available.

### Per-model configuration

Add a `pii:` block to a model YAML to opt in (or out, or to override
per-pattern actions):

```yaml
# Local model — explicit opt-in so chats with this model get redaction
# applied request-side.
name: qwen-7b-local
backend: llama-cpp
pii:
  enabled: true
```

```yaml
# Cloud-bound model — defaults to enabled because backend is cloud-proxy.
# Tighten api_key_prefix from the global default and downgrade email to
# route_local so emails route to a local model rather than leaving the
# network.
name: claude-strict
backend: cloud-proxy
proxy:
  mode: passthrough
  provider: anthropic
  upstream_url: https://api.anthropic.com/v1/messages
  api_key_env: ANTHROPIC_API_KEY
pii:
  patterns:
    - id: api_key_prefix
      action: block        # already the default, made explicit for audit
    - id: email
      action: route_local
```

The regex itself stays global — only the action is settable per-model.
Adding new patterns is a build-time concern (extend `patternRegexps` in
`core/services/routing/pii/patterns.go`).

### NER tier (optional)

The regex matcher covers high-precision patterns. For natural-language
PII (proper names, addresses, organization names) LocalAI carries an
**encoder NER tier** that runs after the regex pass. It expects a
transformers token-classification model wired through the `TokenClassify`
gRPC primitive (e.g. `dslim/bert-base-NER`). The detector annotates
spans with an entity group (`PER`, `LOC`, `ORG`, `MISC`); per-group
actions are configurable through the same `pii:` block.

The NER tier ships as a contract (`NERDetector`, `NERConfig` in
`core/services/routing/pii/ner.go`); an operator-facing knob to load and
attach a detector is not plumbed yet. When no detector is configured the
regex tier still runs.

### Streaming PII filter

Buffered (`/v1/chat/completions` without `"stream": true`) responses are
forwarded verbatim today — only the request-side scan runs. Streaming
responses run through `pii.StreamFilter` which buffers SSE chunks until
either a full pattern matches or the buffer's max length is reached,
then emits the safe prefix. The streaming filter is what makes the
cloud-proxy backend and the MITM proxy safe to expose to clients that
issue streaming requests.

The streaming filter is wired automatically for any model with `pii.enabled`
true — there is no separate streaming toggle.

### Admin page

The `/app/middleware` page (admin role only) has four tabs — **Filtering**,
**Routing**, **MITM Proxy** (see the [MITM doc]({{< relref "mitm-proxy.md" >}})),
and **Events**. The Filtering tab shows:

- The pattern catalogue with live action dropdowns. Changing an action via
  the UI calls `PUT /api/pii/patterns/:id` and updates the live redactor
  in-process. Click **Persist** in the action header to write the current
  state into `runtime_settings.json` so the next process start re-applies it.
- A per-model resolved-state table — each model row reports `enabled`,
  the per-pattern overrides, and which patterns are effectively active.
- A live test panel that posts sample text to `/api/pii/test` and
  highlights matches with their resolved actions, without storing the
  text in the event log.

### REST surface

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/pii/patterns` | any | Live pattern list with current actions. Used by the UI catalogue. |
| POST | `/api/pii/test` | any | Dry-run the redactor on `{"text":"..."}`. Returns hits and the would-be-rewritten body. Does not write to the event log. |
| GET | `/api/pii/events` | admin | Recent middleware events — PII redactions, MITM connect/traffic, admission denials. Filterable by `correlation_id`, `user_id`, `pattern_id`, `kind`. |
| PUT | `/api/pii/patterns/:id` | admin | Update a pattern in-process. Body accepts `{"action":"mask"\|"block"\|"route_local"}` and/or `{"disabled":true\|false}`. Transient — reverts on restart unless persisted. |
| POST | `/api/pii/patterns/persist` | admin | Snapshot the live per-pattern (action, disabled) state into `runtime_settings.json`. |
| GET | `/api/middleware/status` | admin | Aggregated dashboard data: patterns + per-model resolved state + router status + MITM status + admission status. One round-trip for the UI. |

### MCP tools

The same surface is mirrored through the LocalAI Assistant MCP server so
the in-process and stdio assistants can manage the filter conversationally:

| Tool | Read/Write | Purpose |
|---|---|---|
| `list_pii_patterns` | read | Returns the live pattern list. |
| `get_pii_events` | read | Recent redaction / block events with optional filters. |
| `test_pii_redaction` | read | Dry-run sample text without writing to the event log. |
| `get_middleware_status` | read | Aggregator — the same payload as `GET /api/middleware/status`. |
| `set_pii_pattern_action` | write | Update a pattern's action. Admin-only. |
| `persist_pii_patterns` | write | Snapshot live state to `runtime_settings.json`. Admin-only. |

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

On llama-cpp, declare `known_usecases: [score]` on the classifier
model — LocalAI rejects configs that combine `score` with
`chat`/`completion`/`embeddings` there, because the Score RPC races
the `llama_context` against slot-loop traffic.

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
