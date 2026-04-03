+++
disableToc = false
title = "Embeddings"
weight = 13
url = "/features/embeddings/"
+++

LocalAI supports generating embeddings for text or list of tokens.

For the API documentation you can refer to the OpenAI docs: https://platform.openai.com/docs/api-reference/embeddings

## Model compatibility

The embedding endpoint is compatible with `llama.cpp` models, `bert.cpp` models and sentence-transformers models available in huggingface.

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
