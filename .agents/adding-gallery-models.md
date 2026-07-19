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
- **A referenced entry keeps its own gallery row by default.** It is hidden only
  in the collapsed listing (`collapse_variants=true`, the UI's "One row per
  model" toggle), where the declaring entry stands in for it, so referencing an
  entry never makes it unreachable.
- **Order carries no meaning.** Do not try to encode a preference; write the
  list in whatever order reads best.
- **A variant may be smaller than the declaring entry.** Offering a downgrade
  for small hosts is a normal shape: the declaring entry's own build competes
  like every other candidate, so a large host keeps the large build.
- **Do not describe hardware.** At install time LocalAI drops variants whose
  backend cannot run on the host, then drops those that do not fit available
  memory. The declaring entry's own build is exempt from both filters, so
  selection always terminates on something installable. Sizes are measured live
  from the weights and cached, so nothing has to be written down.
- **Engine preference outranks size.** Among the builds that survive the
  filters, the host's preferred engine wins first and only then does the larger
  footprint win. On NVIDIA a vLLM build beats a larger llama.cpp one; on Apple
  silicon an MLX build beats a larger GGUF one; on a host with no preference for
  either engine the larger build wins, since a bigger footprint is a higher
  quality quantization of the same weights. Predict what a user gets by asking
  which engine the host prefers before asking which build is biggest. The
  per-capability order lives in `engineNamePreferenceRules`
  (`pkg/system/capabilities.go`); see
  [adding-backends.md](adding-backends.md) for how a backend gets into it.
- **Serving feature preference sits between engine and size.** Among builds on
  an equally preferred engine, one that speculates or predicts several tokens
  per step beats the plain build of the same weights, because it answers faster
  for the same output: a `-dflash` entry beats an `-mtp` one, and either beats a
  plain build. The order lives in `servingFeaturePreferenceTokens`
  (`pkg/system/capabilities.go`) and is matched against whole segments of the
  variant's entry name, since no gallery field declares it. Engine deliberately
  outranks it: a serving feature makes the right engine faster, it does not make
  a wrong engine right. Fit still outranks both, so a drafter pairing (strictly
  larger than the plain build, since it ships a drafter alongside it) is dropped
  on a host too small for it before this order is ever consulted.
- A variant is nothing but a name; there is no per-variant memory field. When
  the measured size for a build is wrong, correct it on the referenced entry by
  setting that entry's own `size:` (e.g. `size: "20GiB"`). The estimator prefers
  a declared size over its own guesswork, so the fix applies everywhere the size
  is shown or compared rather than only to variant selection.

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
