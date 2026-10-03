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

- the body is larger than 64 KiB,
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

### Initial image policy

This release validates text-only llama.cpp decisions, not full OpenJev image or
projector compatibility. The public routes retain a 64 KiB raw request limit
(413 on overflow), independently of the internal router's serialized request
budget. Image inputs are preserved, with at most eight images and 32 KiB aggregate
encoded data-URL bytes. Only base64 `data:image/...` URLs are accepted. Oversized
image payloads return 413; malformed payloads or excessive image count return 400.
The NER path and native llama.cpp bridge reject image input explicitly with 501;
they never silently discard images. Other native backends receive validated image
fields unchanged and determine their own image support.

Native responses report backend input/output usage, including zero generated
tokens. LocalAI records supplied usage once; explicit zero counts are distinct
from missing usage. Missing counts are not estimated, and invalid negative counts
are rejected rather than billed.
