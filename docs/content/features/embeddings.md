+++
disableToc = false
title = "Embeddings"
weight = 60
url = "/features/embeddings/"
+++

LocalAI supports generating embeddings for text or list of tokens.

For face embeddings specifically, see the
[Face Recognition](/features/face-recognition/) feature - it produces
512-d L2-normalized vectors tuned for face similarity.

For the API documentation you can refer to the OpenAI docs: https://platform.openai.com/docs/api-reference/embeddings

## Model compatibility

The embedding endpoint is compatible with `llama.cpp` models, `bert.cpp` models and sentence-transformers models available in huggingface.

## Choosing the model

Send the model name in the request body. If the body has no `model`, LocalAI uses the configured embeddings model whose name comes first alphabetically. If no embeddings model is configured, the request returns `400 Bad Request`. A model name is never taken from the bearer token unless it matches a configured model name exactly.

## Using Gallery Models

LocalAI provides a model gallery with pre-configured embedding models. To use a gallery model:

1. Ensure the model is available in the gallery (check [Model Gallery]({{%relref "features/model-gallery" %}}))
2. Use the model name directly in your API calls

Example gallery models:
- `qwen3-embedding-4b` - Qwen3 Embedding 4B model
- `qwen3-embedding-8b` - Qwen3 Embedding 8B model  
- `qwen3-embedding-0.6b` - Qwen3 Embedding 0.6B model

### Example: Using Qwen3-Embedding-4B from Gallery

```bash
curl http://localhost:8080/embeddings -X POST -H "Content-Type: application/json" -d '{
  "input": "My text to embed",
  "model": "qwen3-embedding-4b",
  "dimensions": 2560
}'
```

## Manual Setup

Create a `YAML` config file in the `models` directory. Specify the `backend` and the model file.

```yaml
name: text-embedding-ada-002 # The model name used in the API
parameters:
  model: <model_file>
backend: "<backend>"
embeddings: true
```

## Huggingface embeddings

To use `sentence-transformers` and models in `huggingface` you can use the `sentencetransformers` embedding backend.

```yaml
name: text-embedding-ada-002
backend: sentencetransformers
embeddings: true
parameters:
  model: all-MiniLM-L6-v2
```

The `sentencetransformers` backend uses Python [sentence-transformers](https://github.com/UKPLab/sentence-transformers). For a list of all pre-trained models available see here: https://github.com/UKPLab/sentence-transformers#pre-trained-models

{{% notice note %}}

- The `sentencetransformers` backend is an optional backend of LocalAI and uses Python. If you are running `LocalAI` from the containers you are good to go and should be already configured for use.
- For local execution, you also have to specify the extra backend in the `EXTERNAL_GRPC_BACKENDS` environment variable.
    - Example: `EXTERNAL_GRPC_BACKENDS="sentencetransformers:/path/to/LocalAI/backend/python/sentencetransformers/sentencetransformers.py"`
- The `sentencetransformers` backend does support only embeddings of text, and not of tokens. If you need to embed tokens you can use the `bert` backend or `llama.cpp`.
- No models are required to be downloaded before using the `sentencetransformers` backend. The models will be downloaded automatically the first time the API is used.

 {{% /notice %}}

## Llama.cpp embeddings

Embeddings with `llama.cpp` are supported with the `llama-cpp` backend, it needs to be enabled with `embeddings` set to `true`.

```yaml
name: my-awesome-model
backend: llama-cpp
embeddings: true
parameters:
  model: ggml-file.bin
```

Then you can use the API to generate embeddings:

```bash
curl http://localhost:8080/embeddings -X POST -H "Content-Type: application/json" -d '{
  "input": "My text",
  "model": "my-awesome-model"
}' | jq "."
```

## Embedding chat conversations and Go-side pooling

`/v1/embeddings` also accepts a chat conversation via `messages` (a LocalAI
extension), plus a per-request `pooling` scheme that LocalAI applies itself to
the backend's raw per-token vectors:

```bash
curl http://localhost:8080/v1/embeddings -X POST -H "Content-Type: application/json" -d '{
  "model": "my-awesome-model",
  "messages": [
    {"role": "system", "content": "You are a support agent."},
    {"role": "user", "content": "My invoice is wrong."}
  ],
  "pooling": "decayed_mean",
  "pooling_half_life_tokens": 256
}'
```

- One conversation per request; the response is the standard OpenAI embeddings
  shape with a single `data[0].embedding` item.
- `input` and `messages` are mutually exclusive (400 otherwise); an unknown
  `pooling` value is also a 400.
- If the model config carries both `template.chat` and `template.chat_message`,
  the conversation renders exactly like a chat prompt, so the embedding matches
  what a chat model would actually see. Otherwise a frozen role-prefixed
  fallback is used (`<role>: <content>` lines joined by newlines, empty-content
  messages skipped). Non-text content parts (images, audio, video) are ignored.

`pooling` selects how the per-token vectors are reduced to one embedding:

| Value | Meaning |
|-------|---------|
| _(empty)_ / `backend` | The backend pools by itself — the default, today's exact behavior. |
| `mean` | Average of all token vectors. |
| `last` | The last token's vector. |
| `decayed_mean` | Recency-weighted mean: token *i* of *T* weighs `2^(-(T-1-i)/H)` with half-life `H` = `pooling_half_life_tokens` (default 256) — recent turns dominate without erasing earlier context. |

Go-side schemes need raw per-token vectors from the backend. Each backend
declares whether an embedding result is final or per-token; LocalAI rejects a
Go-side scheme for a final vector and rejects `backend` pass-through for a
per-token matrix instead of guessing from its shape. Older backends that do
not declare a layout remain compatible with `backend` pooling only.

llama.cpp chooses this layout when the model is loaded. LocalAI automatically
adds the `pooling:none` backend option when a llama.cpp model sets a Go-side
`parameters.pooling` scheme. That raw-loaded instance can switch between
`mean`, `last`, and `decayed_mean` per request, but it cannot switch back to
`backend` pooling without reloading. Conversely, a backend-pooled llama.cpp
instance rejects per-request Go pooling. Other backends may support Go-side
pooling when they explicitly return per-token vectors.

After Go-side pooling, the vector is normalized with llama.cpp's
`embd_normalize` rule (default L2; configurable through
`options: ["embd_normalize:<n>"]`).

Model-level defaults live under `parameters:`:

```yaml
name: conversation-embedder
backend: llama-cpp
embeddings: true
parameters:
  model: ggml-file.bin
  pooling: decayed_mean
  pooling_half_life_tokens: 256
```

Go-side pooling requires an up-to-date backend that reports its embedding
layout. A legacy backend fails closed for Go-side schemes with an error asking
you to rebuild or update it.

## 💡 Examples

- Example that uses LLamaIndex and LocalAI as embedding: [here](https://github.com/mudler/LocalAI-examples/tree/main/query_data).

## ⚠️ Common Issues and Troubleshooting

### Issue: Embedding model not returning correct results

**Symptoms:**
- Model returns empty or incorrect embeddings
- API returns errors when calling embedding endpoint

**Common Causes:**

1. **Incorrect model filename**: Ensure you're using the correct filename from the gallery or your model file location.
   - Gallery models use specific filenames (e.g., `Qwen3-Embedding-4B-Q4_K_M.gguf`)
   - Check the [Model Gallery]({{%relref "features/model-gallery" %}}) for correct filenames

2. **Context size mismatch**: Ensure your `context_size` setting doesn't exceed the model's maximum context length.
   - Qwen3-Embedding-4B: max 32k (32768) context
   - Qwen3-Embedding-8B: max 32k (32768) context
   - Qwen3-Embedding-0.6B: max 32k (32768) context

3. **Missing `embeddings: true` flag**: The model configuration must have `embeddings: true` set.

**Correct Configuration Example:**

```yaml
name: qwen3-embedding-4b
backend: llama-cpp
embeddings: true
context_size: 32768
parameters:
  model: Qwen3-Embedding-4B-Q4_K_M.gguf
```

### Issue: Dimension mismatch

**Symptoms:**
- Returned embedding dimensions don't match expected dimensions

**Solution:**
- Use the `dimensions` parameter in your API request to specify the output dimension
- Qwen3-Embedding models support dimensions from 32 to 2560 (4B) or 4096 (8B)

```bash
curl http://localhost:8080/embeddings -X POST -H "Content-Type: application/json" -d '{
  "input": "My text",
  "model": "qwen3-embedding-4b",
  "dimensions": 1024
}'
```

### Issue: Model not found

**Symptoms:**
- API returns 404 or "model not found" error

**Solution:**
- Ensure the model is properly configured in the models directory
- Check that the model name in your API request matches the `name` field in the configuration
- For gallery models, ensure the gallery is properly loaded

## Qwen3 Embedding Models Specifics

The Qwen3 Embedding series models have these characteristics:

| Model | Parameters | Max Context | Max Dimensions | Supported Languages |
|-------|------------|-------------|----------------|---------------------|
| qwen3-embedding-0.6b | 0.6B | 32k | 1024 | 100+ |
| qwen3-embedding-4b | 4B | 32k | 2560 | 100+ |
| qwen3-embedding-8b | 8B | 32k | 4096 | 100+ |

All models support:
- User-defined output dimensions (32 to max dimensions)
- Multilingual text embedding (100+ languages)
- Instruction-tuned embedding with custom instructions

## Typed multimodal backend protocol

The backend protocol now defines a dedicated `MultimodalEmbedding` RPC, separate
from the existing text `Embedding` RPC. It carries ordered text and compressed
image/audio/video bytes, selected model identity, inference options, and remaining
request-wide resource allowances. Responses wrap the existing embedding result
with typed resource consumption. Binary media is not encoded into a text prompt.

This protocol addition does **not** enable multimodal HTTP embeddings or establish
native model support. Legacy Go models and old backend servers return gRPC
`UNIMPLEMENTED` for the new RPC; text embeddings continue unchanged. Structured
callers must require protocol version 1 and explicit modality capabilities for
the selected loaded instance. Missing or unknown versions are not evidence of
support. Silent-video and audiovisual-video capabilities are distinct.

See [the backend protocol](../backends/#typed-multimodal-embedding-protocol)
for the wire fields, resource units and compatibility rules.

### Structured embedding routing (protocol groundwork)

Structured embedding sessions use metadata from the selected backend connection,
not a global model-name capability cache. Each request selects a route once;
items stay on that connection. Missing or unknown protocol versions and unavailable
metadata fail closed (501), while known unsupported modalities return 422. There
is no fallback to legacy text embedding. Video track-specific checks remain the
worker's responsibility after probing the compressed media.

Capabilities are rechecked before each item and before returning its result.
Changes require reopening a session; closing a session cancels its outstanding
work without closing the loader-owned connection. Local unload/reload invalidates
the session, while distributed identity refusals reconcile the selected replica,
including refusals during metadata admission before inference. The next request
selects a fresh eligible route; the refused session does not retry elsewhere.
Transient metadata errors do not remove otherwise healthy replica routes.
Legacy text requests keep their existing route. These transport provisions do not
constitute real-model multimodal support; native and full runtime acceptance are
still required.

Protocol version 1 additionally requires an opaque `embedding_generation` token
in metadata and each structured embedding request. The token identifies a worker's
actual loaded-model generation, not a model name, address, or capability hash.
Every successful load/reload issues a new token; unload and failed load invalidate
it. A restarted worker issues independent tokens even at the same address.
Missing tokens fail closed; stale tokens are refused before inference and results
are discarded if their generation changes during execution. A caller must reopen
its session rather than retry an item against stale metadata.

**Native implementation requirement (Task 10):** the llama.cpp adapter must issue
and validate this token under its model lifecycle synchronization, invalidate it
before load/free, and check it at dispatch and result publication. Merely exposing
the protobuf field or unchanged capability flags is insufficient. This Go routing
verification is not native-runtime acceptance.
