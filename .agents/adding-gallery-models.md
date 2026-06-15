# Adding GGUF Models from HuggingFace to the Gallery

When adding a GGUF model from HuggingFace to the LocalAI model gallery, follow this guide.

## Gallery file

All models are defined in `gallery/index.yaml`. Find the appropriate section (embedding models near other embeddings, chat models near similar chat models) and add a new entry.

## Getting the SHA256

GGUF files on HuggingFace expose their SHA256 via the `x-linked-etag` HTTP header. Fetch it with:

```bash
curl -sI "https://huggingface.co/<org>/<repo>/resolve/main/<filename>.gguf" | grep -i x-linked-etag
```

The value (without quotes) is the SHA256 hash. Example:

```bash
curl -sI "https://huggingface.co/ggml-org/embeddinggemma-300m-qat-q8_0-GGUF/resolve/main/embeddinggemma-300m-qat-Q8_0.gguf" | grep -i x-linked-etag
# x-linked-etag: "6fa0c02a9c302be6f977521d399b4de3a46310a4f2621ee0063747881b673f67"
```

**Important**: Pay attention to exact filename casing — HuggingFace filenames are case-sensitive (e.g., `Q8_0` vs `q8_0`). Check the repo's file listing to get the exact name.

## Entry format — Embedding models

Embedding models use `gallery/virtual.yaml` as the base config and set `embeddings: true`:

```yaml
- name: "model-name"
  url: github:mudler/LocalAI/gallery/virtual.yaml@master
  urls:
    - https://huggingface.co/<original-model-org>/<original-model-name>
    - https://huggingface.co/<gguf-org>/<gguf-repo-name>
  description: |
    Short description of the model, its size, and capabilities.
  tags:
    - embeddings
  overrides:
    backend: llama-cpp
    embeddings: true
    parameters:
      model: <filename>.gguf
  files:
    - filename: <filename>.gguf
      uri: huggingface://<gguf-org>/<gguf-repo-name>/<filename>.gguf
      sha256: <sha256-hash>
```

## Entry format — Chat/LLM models

Chat models typically reference a template config (e.g., `gallery/gemma.yaml`, `gallery/chatml.yaml`) that defines the prompt format. Use YAML anchors (`&name` / `*name`) if adding multiple quantization variants of the same model:

```yaml
- &model-anchor
  url: "github:mudler/LocalAI/gallery/<template>.yaml@master"
  name: "model-name"
  icon: https://example.com/icon.png
  license: <license>
  urls:
    - https://huggingface.co/<org>/<model>
    - https://huggingface.co/<gguf-org>/<gguf-repo>
  description: |
    Model description.
  tags:
    - llm
    - gguf
    - gpu
    - cpu
  overrides:
    parameters:
      model: <filename>-Q4_K_M.gguf
  files:
    - filename: <filename>-Q4_K_M.gguf
      sha256: <sha256>
      uri: huggingface://<gguf-org>/<gguf-repo>/<filename>-Q4_K_M.gguf
```

To add a variant (e.g., different quantization), use YAML merge:

```yaml
- !!merge <<: *model-anchor
  name: "model-name-q8"
  overrides:
    parameters:
      model: <filename>-Q8_0.gguf
  files:
    - filename: <filename>-Q8_0.gguf
      sha256: <sha256>
      uri: huggingface://<gguf-org>/<gguf-repo>/<filename>-Q8_0.gguf
```

## Available template configs

Look at existing `.yaml` files in `gallery/` to find the right prompt template for your model architecture:

- `gemma.yaml` — Gemma-family models (gemma, embeddinggemma, etc.)
- `chatml.yaml` — ChatML format (many Mistral/OpenHermes models)
- `deepseek.yaml` — DeepSeek models
- `virtual.yaml` — Minimal base (good for embedding models that don't need chat templates)

## Checklist

1. **Find the GGUF file** on HuggingFace — note exact filename (case-sensitive)
2. **Get the SHA256** using the `curl -sI` + `x-linked-etag` method above
3. **Choose the right template** config from `gallery/` based on model architecture
4. **Add the entry** to `gallery/index.yaml` near similar models
5. **Set `embeddings: true`** if it's an embedding model
6. **Include both URLs** — the original model page and the GGUF repo
7. **Write a description** — mention model size, capabilities, and quantization type
