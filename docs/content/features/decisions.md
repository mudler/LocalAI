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
follow the [kev](https://github.com/jaredpalmer/kev) project and match the
`/v1/systemone` endpoint that Ollama added in 0.35. The wire contract is called
SystemOne; the capability a model declares is called `decisions`.

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

The engine, [vllm.cpp]({{% relref "features/vllm-cpp" %}}), also supports the
kev, CLM and xor decision models. Those checkpoints need a conversion step, so
they are not gallery entries yet.

Tev1 is an autoregressive decision model. It answers through chat completions
and does not serve `/v1/systemone` yet.

## Access control

When authentication is on, the three routes need the `decisions` feature. It is
on by default for every user, like the other API features, and an administrator
can turn it off per user.
