+++
disableToc = false
title = "Setting Up Models"
weight = 3
icon = "hub"
description = "Learn how to install, configure, and manage models in LocalAI"
+++

![Model resolution: many sources converge on one resolve, auto-detect backend, load, and serve path](/images/diagrams/model-resolution.png)

This section covers everything you need to know about installing and configuring models in LocalAI. You'll learn multiple methods to get models running.

## Prerequisites

- LocalAI installed and running (see [Quickstart]({{% relref "getting-started/quickstart" %}}) if you haven't set it up yet)
- Basic understanding of command line usage

## Method 1: Using the Model Gallery (Easiest)

The Model Gallery is the simplest way to install models. It provides pre-configured models ready to use.

GPU recommendations require a memory estimate within 95% of the detected model memory budget at a 4096-token context. If none of the sampled candidates fit, the recommendation section is hidden. You can still browse the gallery and check individual models at your intended context size. On the Models page the recommendations appear as a "Best for this machine" list in the pane beside the table while no model is selected. Once you have installed a model, the list shows only the best fit and offers the others behind "more that fit". The Home page also omits static GPU suggestions when no fitting recommendation is available.

### Via WebUI

1. Open the LocalAI WebUI at `http://localhost:8080`
2. Navigate to **Models → Explore**
3. Browse or search the available models
4. Click "Install" on any model you want
5. Wait for installation to complete. Progress appears in the strip at the top
   of the app, and **Operate → Activity** shows every install in flight, plus
   what failed and what finished (see [Activity]({{% relref "operations/activity" %}}))

For more details, refer to the [Gallery Documentation]({{% relref "features/model-gallery" %}}).

The same Models page owns the complete lifecycle. Switch to **Installed** to
search local configurations, filter them by running, idle, disabled, pinned,
or distributed state, and open a model's runtime controls. Load and stop are on
each row; edit, pin, disable, backend logs, and remove are in the row menu and in
the details of the selected model. The current view, search, filter, and
selection are stored in the URL so links and browser history preserve your place.
The disk strip in the page header shows the free space on the models disk and
opens a review of what can be removed to free more (see
[Model gallery]({{% relref "features/model-gallery" %}})).

### Via CLI

```bash
# List available models
local-ai models list

# Install a specific model
local-ai models install llama-3.2-1b-instruct:q4_k_m

# Start LocalAI with a model from the gallery
local-ai run llama-3.2-1b-instruct:q4_k_m
```

To run models available in the LocalAI gallery, you can use the model name as the URI. For example, to run LocalAI with the Hermes model, execute:

```bash
local-ai run hermes-2-theta-llama-3-8b
```

To install only the model, use:

```bash
local-ai models install hermes-2-theta-llama-3-8b
```

Note: The galleries available in LocalAI can be customized to point to a different URL or a local directory. For more information on how to setup your own gallery, see the [Gallery Documentation]({{% relref "features/model-gallery" %}}).

### Browse Online

Visit [models.localai.io](https://models.localai.io) to browse all available models in your browser.

## Method 1.5: Import Models via WebUI

The WebUI import page (**Build → Import**) takes either a source to resolve or a
configuration to write. Both live on the same page, behind the two tabs in its
header. From a source, it is a short guided flow: source, review, import, done.

### From a source

1. Open the LocalAI WebUI at `http://localhost:8080`
2. Open **Build**, then **Import**
3. Paste the source into the **Source** field (e.g. `https://huggingface.co/Qwen/Qwen3-VL-8B-Instruct-GGUF`), or start from one of the examples
4. Review what the page says, then press Enter or click **Import**

While you type, the page reads the spelling of the source (Hugging Face
repository, direct URL, configuration file, OCI image, Ollama model, or a path
on the host) and shows the request the form will send. It also runs the checks
that can be made before the import starts: whether the name is already in use,
whether the chosen backend is installed, and how much disk and memory are free.
The server returns no preview of the model configuration, the size or the memory
a model needs before the import starts, so the page does not show them. They
appear once the import starts, next to the free memory and disk.

The **What you can paste** panel beside the field lists every accepted scheme:
`huggingface://`, `hf://`, a full Hugging Face URL, any direct `https://` URL,
`file://` and absolute paths on the host, `oci://`, `ocifile://`, and
`ollama://`.

Expanding **Import options** reveals everything you can override before the
import runs: backend, name, description, quantizations, MMProj quantizations,
model type, embeddings support, the diffusers-specific fields, and arbitrary
custom key-value preferences. The backend list can be narrowed by modality
first. Fields that the selected backend cannot use are hidden, and anything you
typed into them is kept in case you switch back.

Leaving the backend on auto-detect lets LocalAI choose from the source. If more
than one installed backend can serve the detected modality, the page says so
and offers the candidates inline — picking one resubmits the import.

Repositories under `mlx-community` are imported with the native MLX backend.
LocalAI uses Hugging Face's pipeline metadata to select `mlx-vlm` for
vision-language models and `mlx-audio` for text-to-speech models; other MLX
repositories use `mlx`. An explicit backend selection in the import form always
overrides this automatic routing.

Once the import starts, the page reports the download size and the memory the
model needs against what is free, then the current phase, the bytes transferred
and a progress bar. When the model is ready, the page names it and links to a
chat with it and to Models.

### Writing YAML

For full control over model configuration, switch to the **Write YAML** tab and
edit the configuration directly, then click **Create**. The editor provides
syntax highlighting and a copy button, and accepts the same configuration keys
documented under [Advanced]({{% relref "advanced" %}}).

This is especially useful for:
- Custom model configurations
- Fine-tuning model parameters
- Setting up complex model setups
- Editing existing model configurations

## Method 2: Installing from Hugging Face

LocalAI can directly install models from Hugging Face:

```bash
# Install and run a model from Hugging Face
local-ai run huggingface://TheBloke/phi-2-GGUF
```

The format is: `huggingface://<repository>/<model-file>` (<model-file> is optional)

### Examples

```bash
local-ai run huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf
```

## Method 3: Installing from OCI Registries

### Ollama Registry

```bash
local-ai run ollama://gemma:2b
```

### Standard OCI Registry

```bash
local-ai run oci://localai/phi-2:latest
```

{{% notice note %}}
On every model download — Ollama and OCI registries, the model gallery, and plain HTTP(S) file URLs alike — LocalAI identifies itself with a `LocalAI/<version> (<os>; <arch>)` `User-Agent` header (for example `LocalAI/v3.2.1 (linux; amd64)`) so registry and gallery operators can attribute usage to LocalAI. Builds from source that carry no stamped version send `LocalAI (<os>; <arch>)` instead.
{{% /notice %}}

### Run Models via URI

To run models via URI, specify a URI to a model file or a configuration file when starting LocalAI. Valid syntax includes:

- `file://path/to/model` (absolute path to a file within your models directory)
- `huggingface://repository_id/model_file` (e.g., `huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf`)
- From OCIs: `oci://container_image:tag`, `ollama://model_id:tag`
- From configuration files: `https://gist.githubusercontent.com/.../phi-2.yaml`

{{% notice note %}}
When using `file://` URLs, the path must point to a file within your models directory (specified by `MODELS_PATH`). Files outside this directory are rejected for security reasons.
{{% /notice %}}

Configuration files can be used to customize the model defaults and settings. For advanced configurations, refer to the [Customize Models section]({{% relref "getting-started/customize-model" %}}).

### Examples

```bash
local-ai run huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf
local-ai run ollama://gemma:2b
local-ai run https://gist.githubusercontent.com/.../phi-2.yaml
local-ai run oci://localai/phi-2:latest
```

## Method 4: Manual Installation

For full control, you can manually download and configure models.

### Step 1: Download a Model

Download a GGUF model file. Popular sources:

- [Hugging Face](https://huggingface.co/models?search=gguf)

Example:

```bash
mkdir -p models

wget https://huggingface.co/TheBloke/phi-2-GGUF/resolve/main/phi-2.Q4_K_M.gguf \
  -O models/phi-2.Q4_K_M.gguf
```

### Step 2: Create a Configuration File (Optional)

Create a YAML file to configure the model:

```yaml
# models/phi-2.yaml
name: phi-2
parameters:
  model: phi-2.Q4_K_M.gguf
  temperature: 0.7
context_size: 2048
threads: 4
backend: llama-cpp
```

Customize model defaults and settings with a configuration file. For advanced configurations, refer to the [Advanced Documentation]({{% relref "advanced" %}}).

### Step 3: Run LocalAI

Choose one of the following methods to run LocalAI:

{{< tabs >}}
{{% tab title="Docker" %}}

```bash
mkdir models

cp your-model.gguf models/

docker run -p 8080:8080 -v $PWD/models:/models -ti --rm localai/localai:latest --models-path /models --context-size 700 --threads 4

curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.gguf",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

{{% notice tip %}}
**Other Docker Images**:

For other Docker images, please refer to the table in [the container images section]({{% relref "getting-started/containers" %}}).
 {{% /notice %}}

### Example:

```bash
mkdir models

wget https://huggingface.co/TheBloke/Luna-AI-Llama2-Uncensored-GGUF/resolve/main/luna-ai-llama2-uncensored.Q4_0.gguf -O models/luna-ai-llama2

cp -rf prompt-templates/getting_started.tmpl models/luna-ai-llama2.tmpl

docker run -p 8080:8080 -v $PWD/models:/models -ti --rm localai/localai:latest --models-path /models --context-size 700 --threads 4

curl http://localhost:8080/v1/models

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "luna-ai-llama2",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9
   }'
```

{{% notice note %}}
- If running on Apple Silicon (ARM), it is **not** recommended to run on Docker due to emulation. Follow the [build instructions]({{% relref "getting-started/build" %}}) to use Metal acceleration for full GPU support.
- If you are running on Apple x86_64, you can use Docker without additional gain from building it from source.
 {{% /notice %}}

{{% /tab %}}
{{% tab title="Docker Compose" %}}

```bash
git clone https://github.com/go-skynet/LocalAI

cd LocalAI

cp your-model.gguf models/

docker compose up -d --pull always

curl http://localhost:8080/v1/models

curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.gguf",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

{{% notice tip %}}
**Other Docker Images**:

For other Docker images, please refer to the table in [Getting Started](https://localai.io/basics/getting_started/#container-images).
 {{% /notice %}}

Note: If you are on Windows, ensure the project is on the Linux filesystem to avoid slow model loading. For more information, see the [Microsoft Docs](https://learn.microsoft.com/en-us/windows/wsl/filesystems).

{{% /tab %}}
{{% tab title="Kubernetes" %}}

For Kubernetes deployment, see the [Kubernetes installation guide]({{% relref "getting-started/kubernetes" %}}).

{{% /tab %}}
{{% tab title="From Binary" %}}

LocalAI binary releases are available on [GitHub](https://github.com/go-skynet/LocalAI/releases).

```bash
# With binary
local-ai --models-path ./models
```

{{% notice tip %}}
If installing on macOS, you might encounter a message saying:

> "local-ai-git-Darwin-arm64" (or the name you gave the binary) can't be opened because Apple cannot check it for malicious software.

Hit OK, then go to Settings > Privacy & Security > Security and look for the message:

> "local-ai-git-Darwin-arm64" was blocked from use because it is not from an identified developer.

Press "Allow Anyway."
 {{% /notice %}}

{{% /tab %}}
{{% tab title="From Source" %}}

For instructions on building LocalAI from source, see the [Build from Source guide]({{% relref "getting-started/build" %}}).

{{% /tab %}}
{{< /tabs >}}

### GPU Acceleration

For instructions on GPU acceleration, visit the [GPU Acceleration]({{% relref "features/gpu-acceleration" %}}) page.

For more model configurations, visit the [Examples Section](https://github.com/mudler/LocalAI-examples/tree/main/configurations).

## Understanding Model Files

### File Formats

- **GGUF**: Modern format, recommended for most use cases
- **GGML**: Older format, still supported but deprecated

### Quantization Levels

Models come in different quantization levels (quality vs. size trade-off):

| Quantization | Size | Quality | Use Case |
|-------------|------|---------|----------|
| Q8_0 | Largest | Highest | Best quality, requires more RAM |
| Q6_K | Large | Very High | High quality |
| Q4_K_M | Medium | High | Balanced (recommended) |
| Q4_K_S | Small | Medium | Lower RAM usage |
| Q2_K | Smallest | Lower | Minimal RAM, lower quality |

### Choosing the Right Model

Consider:

- **RAM available**: Larger models need more RAM
- **Use case**: Different models excel at different tasks
- **Speed**: Smaller quantizations are faster
- **Quality**: Higher quantizations produce better output

## Model Configuration

### Basic Configuration

Create a YAML file in your models directory:

```yaml
name: my-model
parameters:
  model: model.gguf
  temperature: 0.7
  top_p: 0.9
context_size: 2048
threads: 4
backend: llama-cpp
```

### Advanced Configuration

See the [Model Configuration]({{% relref "advanced/model-configuration" %}}) guide for all available options.

## Managing Models

### List Installed Models

Ollama clients can list configured models with `GET /api/tags` and loaded models with `GET /api/ps`. Each entry includes `size` in bytes when LocalAI can resolve a non-empty primary weights file on disk. This is the size of that file, not the total size of a multi-file model or its memory use. Unknown sizes are omitted; `/api/ps` also omits `size_vram` because per-model VRAM use is not available.

```bash
# Via API
curl http://localhost:8080/v1/models

# Via CLI
local-ai models list
```

### Disk Usage

In the WebUI, the **Installed** tab of **Models** shows how much disk each
installed configuration uses. The **Size** column holds the size on disk, and
for a model that shares files with others it adds the shared part ("1.2 GB
shared"). A file referenced by more than one configuration is counted once in
the total under the table, which also gives the shared total. The inspector
beside the table names the models a model shares files with, and a model page
lists its files under **Usage and history**, with each file's size, the other
models that use it, and whether it is missing. Removing a model frees only its
exclusive files; the files it shares stay while another configuration uses
them. A reference whose file is not on disk (a download that never finished, or
a file removed by hand) is marked on the model and in the cleanup review.

Only an admin can read the report. For anyone else, or if the read fails, the
Size column shows the size of the files the gallery lists, and "size unknown"
for a model the gallery does not list.

The report behind these views is available directly (admin only):

```bash
curl http://localhost:8080/api/models/storage
```

It returns per-model sizes with the shared portion split out, the
file-to-models relation, and missing references. Files in the models directory
that no configuration references are not included.

### Remove Models

Simply delete the model file and configuration from your models directory:

```bash
rm models/model-name.gguf
rm models/model-name.yaml  # if exists
```

## Troubleshooting

### Model Not Loading

1. **Check backend**: Ensure the required backend is installed

   ```bash
   local-ai backends list
   local-ai backends install llama-cpp  # if needed
   ```

2. **Check logs**: Enable debug mode

   ```bash
   DEBUG=true local-ai
   ```

3. **Verify file**: Ensure the model file is not corrupted

### Out of Memory

- Use a smaller quantization (Q4_K_S or Q2_K)
- Reduce `context_size` in configuration
- Close other applications to free RAM

### Wrong Backend

Check the [Compatibility Table]({{% relref "reference/compatibility-table" %}}) to ensure you're using the correct backend for your model.

## Best Practices

1. **Start small**: Begin with smaller models to test your setup
2. **Use quantized models**: Q4_K_M is a good balance for most use cases
3. **Organize models**: Keep your models directory organized
4. **Backup configurations**: Save your YAML configurations
5. **Monitor resources**: Watch RAM and disk usage
