+++
disableToc = false
title = "Decisions API"
weight = 66
url = "/features/decisions/"
+++

The Decisions API is a fast, typed decision layer. You send a piece of text (the
*state*) and a set of named questions. A decision model answers each question
with a value and a confidence, in one pass. The model does not generate text, so
there is nothing to parse and no free-form output to validate.

LocalAI serves it on the `/v1/systemone` routes. The request and response shapes
follow the [kev](https://github.com/jaredpalmer/kev) project, and the field names
and question types are the same ones Ollama serves on its `/v1/systemone`
endpoint (Ollama 0.35 and later). The wire contract is called SystemOne; the
capability a model declares is called `decisions`. See
[Compatibility with Ollama](#compatibility-with-ollama) for what differs.

OpenAI announced its own Decisions API in limited preview on 2026-09-29. It has no
public request or response schema yet, so LocalAI does not serve a `/v1/decisions`
route.

## Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/v1/systemone` | POST | Answer all questions in one pass |
| `/v1/systemone/permute` | POST | Re-run one choice question under `n_perm` option orders |
| `/v1/systemone/separate` | POST | Answer each question in its own pass |

Which route a model can serve depends on its kind:

| Model kind | `/v1/systemone` | `/permute` and `/separate` |
|---|---|---|
| Decision model (`decisions`), such as Laya or GLiNER2.5-Decide | Yes | No, returns `400` |
| Zero-shot NER model (`token_classify`), such as GLiNER2.5 | Yes, through the NER path | Yes |

## Question types

| Type | Answer | Fields in the answer |
|---|---|---|
| `choice` | One option out of a named set | `choice`, `probabilities`, `confidence` |
| `noul` | Yes, no or unknown for a statement | `noul` (0 to 1), `entities` |
| `score` | One level on a scale | `score`, `legend`, `probabilities`, `confidence` |

## Example

```bash
curl http://localhost:8080/v1/systemone -H "Content-Type: application/json" -d '{
  "model": "laya-vllm-cpp",
  "state": "My order arrived broken and I want my money back. This is the second time.",
  "questions": {
    "team": {
      "type": "choice",
      "instructions": "Which team should handle this ticket?",
      "criteria": {
        "billing": "Payments, invoices and refunds",
        "shipping": "Delivery and damaged goods",
        "product": "Questions about how the product works"
      }
    },
    "refund_requested": {
      "type": "noul",
      "instructions": "The customer explicitly asks for a refund"
    },
    "urgency": {
      "type": "score",
      "instructions": "How urgent is this ticket?",
      "criteria": ["not urgent", "somewhat urgent", "urgent", "critical"]
    }
  }
}'
```

Answers from a decision model carry a `confidence` value, and the response
reports token usage and `latency_ms`. The NER path does not report token usage.

## Choosing a model

A model can serve the Decisions API only if it is a decision model. Declare the usecase
in the model config:

```yaml
name: laya
backend: vllm-cpp
known_usecases:
  - decisions
parameters:
  model: convaiinnovations/laya
```

`decisions` is never guessed, and a model that declares it is not listed as a
chat, completion or embeddings model. A model that declares usecases without
`decisions` or `token_classify` gets a `400` from these endpoints that names the
missing usecase. A model that declares `token_classify` and not `decisions` is
served by the zero-shot NER path. A vllm-cpp config that declares no usecases is
treated as a decision model, so setups that predate the flag keep working, but a
config that declares only `chat` (as an older `laya` gallery entry did) now gets
the `400` and needs `known_usecases: [decisions]`.

Install one from the gallery and filter on the `decisions` tag:

| Gallery entry | Model | Notes |
|---|---|---|
| `laya-vllm-cpp` | Laya | ModernBERT-large, non-autoregressive, about 800 MB |
| `gliner25-decide-vllm-cpp` | GLiNER2.5-Decide | DeBERTa-v3-large with a classification head, about 2 GB |
| `tev1-4b-vllm-cpp` | Tev1 4B | Autoregressive Qwen3.5-4B fine-tune that answers with an option letter, about 9.3 GB |
| `tev1-0.8b-vllm-cpp` | Tev1 0.8B | Autoregressive Qwen3.5-0.8B fine-tune that answers with an option letter, about 1.8 GB |
| `kev-0.8b-vllm-cpp` | kev 0.8B | Qwen3.5-0.8B-Base with a merged LoRA and a PointerHead readout, converted for vllm.cpp only, about 1.53 GB |
| `nimble-9b-vllm-cpp` | Bespoke Nimble 9B | Qwen3.5-9B with the Nimble LoRA merged, reads the answer-letter logits, converted for vllm.cpp only, about 19.3 GB |
| `clm-v0.1-8b-vllm-cpp` | CLM v0.1 8B | Bi-encoder: Qwen3-8B backbone with state and action heads, answers by cosine similarity, converted for vllm.cpp only, about 16.5 GB |

The engine, [vllm.cpp]({{% relref "features/vllm-cpp" %}}), also supports the
xor decision model. That checkpoint needs a conversion step, so it is not a
gallery entry yet. The kev, Nimble and CLM entries install checkpoints that
were already converted with the vllm.cpp `convert-kev.py`, `convert-nimble.py`
and `convert-clm.py` scripts.

Nimble refuses a question with more than 26 choices (the upstream release
allows 255). On CPU it needs about 20 GB of free RAM. For CLM, put the
question in `instructions`: the state head reads the state followed by the
instructions. On CPU it needs about 19 GB of free RAM.

Tev1 is an autoregressive decision model. The engine answers each question by
scoring the option letters, so its `confidence` is the entropy measure Ollama
uses. A Tev1 `choice` or `score` question accepts at most 24 options (Ollama
allows 26), because the model is trained on the letters A to X, and every
option needs a nonempty description. The published checkpoints name another
architecture in `config.json`, so the Tev1 gallery entries set
`engine_args.hf_overrides` to load them as `Tev1Model` (see
[Overriding config.json keys]({{% relref "features/vllm-cpp" %}}#overriding-configjson-keys-hf_overrides)).
The same model also answers `/v1/chat/completions` requests.

## Request limits

A request is refused with `400` (or `413` for the body size) when:

- a text-only body is larger than 64 KiB (image-bearing bodies have the bounded budget below),
- `state` is missing or blank,
- there are no questions, or more than 64,
- a question id is blank,
- a `choice` question has fewer than 2 options or a blank option key,
- a `score` question has fewer than 2 levels,
- a `noul` question has `criteria` with keys other than `"false"` and `"true"`.

A `noul` question may carry `criteria` with a description for each outcome, for
example `{"false": "No refund is requested", "true": "The customer requests a refund"}`.
Some models cap the number of options for a `choice` or `score` question. Models
that answer with a letter accept at most 26, and Tev1 accepts at most 24. The engine refuses more options than
the model supports and the error names the limit.

## Compatibility with Ollama

The field names, question types and answer fields are the same as Ollama's
`/v1/systemone`, so a client written for one works against the other for the
common case. These behaviors differ:

| | Ollama | LocalAI |
|---|---|---|
| `confidence` | `1 - H(p) / ln(N)`, an entropy measure | Computed by the model's pipeline. For kev and Laya it is a normalized margin, so the same probabilities give a different value |
| Errors | `{"error": "message"}` | `{"error": {"message": "...", "type": "invalid_request"}}` |
| `keep_alive` | Sets how long the model stays loaded | Accepted and ignored. Model lifetime follows the LocalAI idle and watchdog settings |
| `state` given as an object | Serialized as JSON text | Rendered as labeled lines, the way kev does it |
| `noul` answer on the NER path | `{type, noul}` | Also carries `entities` |
| Token `usage` | Full prompt lengths across all questions | Whatever the backend reports; the NER path reports 0 |

## Access control

When authentication is on, the three routes need the `decisions` feature. It is
on by default for every user, like the other API features, and an administrator
can turn it off per user.

## Native llama.cpp decisions

The stock `llama-cpp` backend supports text-only decision GGUFs carrying upstream
SystemOne metadata. Declare `known_usecases: [decisions]`; ordinary `score` need
not be enabled. Requests use the existing internal Score RPC, not a backend HTTP
server. Choice, score, and noul questions may be combined in one request. Structured
state and questions are forwarded without NER rendering. llama.cpp score questions
accept 2–10 levels; this backend-specific limit does not constrain vllm-cpp.
Older forks without native decision support return 501. Missing decision metadata
also returns 501, while backend invalid requests return 400.

### Bounded image input

The public routes and internal decision validator share an image contract. Supply
PNG or JPEG base64 data URLs in `images`, OpenAI `image_url` message content, or
Anthropic `image` content with a `base64` source and `media_type`. Remote URLs and
file paths are never fetched. MIME must match the decoded image header; malformed
base64, unsupported formats and invalid headers return 400.

Limits per request are **8 images**, **12 MiB aggregate encoded data-URL bytes**,
**8 MiB aggregate decoded bytes**, **4096 pixels per dimension**, and
**16 million aggregate pixels**. Exceeding these limits returns 413. Headers are
checked before full pixel decoding, which rejects truncated or corrupt images; native decoders must independently
protect direct RPC inputs.

Image-bearing request bodies may use up to **16 MiB**. Text-only requests retain
the **64 KiB raw-wire limit**, including whitespace; JSON escaping during internal
serialization does not impose a second HTTP limit. Absent, `null`, or empty
`images` do not enable the larger budget. Native decision responses retain a
separate **64 KiB** limit, independent of the request budget. These limits do not
raise any global HTTP limit.

A shared **8-request admission ceiling** covers public decision handlers and
internal decision runners before body buffering, image decoding or serialization.
Saturation fails promptly (HTTP 503); cancellation before admission does not take
a slot. A slot remains held through inference/response handling, and an internal
cancelled call retains its slot until its underlying worker actually ends. This
bounds concurrent decision-owned allocation and retained request bodies, not total
process memory: caller-owned inputs, upstream middleware buffers and model/backend
memory are outside this budget. Full decoding is sequential per admitted request,
with each pixel buffer limited by the checked dimensions and aggregate pixel
budget (up to 16 million pixels; decoded byte storage depends on pixel format).
Garbage collection timing is not an RSS guarantee. The separate 8-operation backend ceiling still
bounds abandoned native operations. Validation helpers do not acquire nested slots.

For image-only input, provide explicit structured state such as `"state": {}`
alongside `images`, or a message containing image content. Missing, null or blank
string state remains invalid. Arbitrary domain JSON is preserved, not interpreted
as image content outside message content parts. Images are never replaced by
invented text.

Admission is not a promise of model image capability: the NER path and currently
installed text-only native decision bridges reject images with 501 rather than
silently dropping them. Other native backends receive validated fields unchanged
and determine their image support. Failed requests are not billed. Router image
preservation and native OpenJev/projector validation are separate follow-up work.

Native responses report backend input/output usage, including zero generated
tokens. LocalAI records supplied usage once; explicit zero counts are distinct
from missing usage. Missing counts are not estimated, and invalid negative counts
are rejected rather than billed.

### Julia-1 CPU example

Install the separate stock llama.cpp entry (existing vllm-cpp entries are unchanged):

```sh
local-ai models install julia-1-llama-cpp
```

Julia-1 is a 144.3M-parameter multilingual text decision model. The gallery pins
`ggml-org/Julia-1-GGUF` revision `16fee17949206fbf58da9347daea44d792a81211`,
file `Julia-1-Q8_0.gguf` (168,166,496 bytes, about 160.4 MiB), SHA-256
`1ea6a7e87156eeeda88cb7a36a61265b37ba7b993897b7289b99aea5b5e47069`.
The source model and GGUF publisher declare Apache-2.0. Source provenance:
`SupersonicLabs/Julia-1` revision `a85b127321d580d65176c89ced8273f305745d85`,
based on `jhu-clsp/mmBERT-small`. This is a real model, not the upstream tiny test
fixture; assess its accuracy for your own tasks.

```sh
curl http://localhost:8080/v1/systemone \
  -H 'Content-Type: application/json' \
  -d '{"model":"julia-1-llama-cpp","state":"I was charged twice and need a refund.","questions":{"route":{"type":"choice","instructions":"Which team should handle this?","criteria":{"billing":"payments and refunds","shipping":"delivery problems","technical":"software issues"}},"refund":{"type":"noul","instructions":"Does the customer request a refund?"}}}'
```

The pinned artifact was installed through the gallery installer, checksum-verified,
and tested on CPU with the native Score RPC using choice, score, and noul in one
request. That smoke returned 97 input tokens and zero output tokens; token counts
vary with the request. This does not establish broad model accuracy or image
support.

### Native family defaults

Each family has a separately named default; these do not replace vllm-cpp entries.
All entries are text-only and omit projectors. Download size is not a RAM estimate.

| Gallery entry | Quantization | Artifact bytes | License | Validation status |
|---|---|---:|---|---|
| `julia-1-llama-cpp` | Q8_0 | 168,166,496 | Apache-2.0 | Gallery install and CPU request verified |
| `laya-llama-cpp` | Q8_0 | 449,397,600 | Apache-2.0 | Gallery install and CPU choice/score/noul verified |
| `kev-4b-llama-cpp` | Q4_K_M | 3,033,489,824 | Apache-2.0 | Gallery install and CPU choice/score/noul verified |
| `lev-llama-cpp` | Q4_K_M | 3,011,777,440 | Apache-2.0 | Gallery install and CPU choice/score/noul verified |
| `openjev-llama-cpp` | Q4_K_M | 18,973,872,288 | **CC-BY-NC-4.0** | Gallery install and CPU choice/score/noul verified |
| `nimble-9b-v3-llama-cpp` | Q4_K_M | 6,324,185,632 | **CC-BY-NC-4.0** | Gallery install and CPU choice/score/noul verified |

OpenJev and Nimble are noncommercial models. OpenJev's upstream multimodal
capability does **not** imply LocalAI decision-image support. Nimble requires the
native Nimble integration included in this source tree's llama.cpp pin
`bed0a856606ee4a24a164066f73d2379447033f5`; older installed backends must be
updated before serving it. This source prerequisite is integrated, but the
OpenJev and Nimble installation/runtime checks remain pending as listed above.
The published entries pin revisions and SHA-256 checksums, but metadata verification
alone is not a runtime test. No model-quality guarantee follows from these smoke
tests. Laya, Kev-4B, and lev were also retested against the newer native backend
with 1- and 11-level score requests correctly rejected.

OpenJev and Nimble validation used the native backend at llama.cpp revision
`bed0a856606ee4a24a164066f73d2379447033f5`, CPU-only with two threads, a 2048-token
context, and batch size 512. Each artifact was installed through the gallery,
SHA-256 verified, and checked for its decision metadata and SystemOne template.
Each request included choice, score, and noul questions together, including the
full question set required by Nimble. Response-shape, probability-normalization,
and noul-bound assertions passed; 1- and 11-level score requests were rejected.
The test requests reported 224 input tokens for OpenJev and 933 for Nimble, with
explicit zero output tokens for both. No projector was installed or tested.

These are bounded text contract smoke tests, not accuracy benchmarks or
performance guarantees. Floating-point probabilities can vary with hardware and
build settings; tests do not require exact answer probabilities or token counts.
Neither image support nor interruption during active evaluation is established
by these tests. CC-BY-NC-4.0's noncommercial restriction still applies.

### Multimodal router probes

The `decisions` router classifier preserves ordered OpenAI message content and
Anthropic base64 image sources as structured state, including image-only turns.
It uses the internal decision runner, not a loopback HTTP request. The shared
image limits above are validated before model loading; no URL is fetched by the
classifier. Original message content is not rewritten when selecting a candidate
or the configured fallback.

Score, rerank and KNN classifiers are text-only: image input produces an explicit
classifier error and follows the existing configured fallback policy, rather
than classifying an image-stripped prompt. Without a fallback, routing fails.
Parent cancellation remains terminal and does not select a fallback. Image probes
bypass text embedding caches and are not trimmed to text-only turns. Native
context overflow is reported by the backend rather than silently dropping images.
These transport guarantees do not establish installed projector capability or
real-model image accuracy; those require separate native and end-to-end validation.

OpenAI chat routing classifies the original structured message before preparing
media for the selected model. Remote image URLs are not downloaded as decision
inputs. After selection (including a configured fallback), the served model's
normal media preparation runs without replacing the original content blocks.
Invalid classifier configuration fails closed even with a configured fallback.
Runtime classification and input errors follow the configured fallback policy.
Cancellation never selects a fallback.

Router probe extraction checks the shared 16 MiB state budget before copying
text or serializing messages, including JSON escaping expansion. This applies
to typed and untyped internal requests as well as parsed API requests; it does
not add a limit to non-router inference. Direct internal probes containing
custom JSON/text marshalers or excessively nested values fail extraction rather
than executing unbounded serialization. The separate 64 KiB text-only Decisions
request limit is unchanged. Anthropic conversion preserves typed content blocks
through both native selection and fallback, including ordered text and images.
