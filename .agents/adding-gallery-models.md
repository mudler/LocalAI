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

## Offering several builds of one model (`variants`)

When the same model is published in more than one quantization, or is also
servable by another engine, add each build as its own ordinary gallery entry and
then point one of them at the others with `variants`:

```yaml
- !!merge <<: *chatml
  name: "nanbeige4.1-3b-q4"
  # ... the usual urls / overrides / files for the Q4 build ...
  variants:
    - model: nanbeige4.1-3b-q8
```

Rules:

- The declaring entry is a **complete, normal entry**. It keeps its own
  `files`/`overrides` and stays installable on every host and by every older
  LocalAI release, which simply ignore `variants`.
- A variant references another gallery entry **by name**. That entry must exist
  and must not declare `variants` of its own.
- **Order carries no meaning.** Do not try to encode a preference; write the
  list in whatever order reads best.
- **Do not describe hardware.** At install time LocalAI drops variants whose
  backend cannot run on the host, drops those that do not fit available memory,
  and installs the largest survivor, falling back to the declaring entry's own
  build. Sizes are measured live from the weights and cached, so nothing has to
  be written down.
- `min_memory` on a single variant (e.g. `min_memory: 20GiB`) overrides the
  measured size and suppresses the measurement for that variant. Use it only
  when you have measured a real load and know the estimate is wrong; most
  variants should not carry it.

Users can override the automatic choice with `variant` on `POST /models/apply`,
`local-ai models install --variant`, or the `install_model` MCP tool. See
`docs/content/features/model-gallery.md`.

The gallery lint specs live in `core/gallery`, so run that suite after adding a
`variants` list.

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
