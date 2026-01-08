+++
disableToc = false
title = "Setting Up Models"
weight = 2
icon = "hub"
description = "Learn how to install, configure, and manage models in LocalAI"
+++

This section covers everything you need to know about installing and configuring models in LocalAI. You'll learn multiple methods to get models running.

## Prerequisites

- LocalAI installed and running (see [Quickstart]({{% relref "getting-started/quickstart" %}}) if you haven't set it up yet)
- Basic understanding of command line usage

## Method 1: Using the Model Gallery (Easiest)

The Model Gallery is the simplest way to install models. It provides pre-configured models ready to use.

### Via WebUI

1. Open the LocalAI WebUI at `http://localhost:8080`
2. Navigate to the "Models" tab
3. Browse available models
4. Click "Install" on any model you want
5. Wait for installation to complete

For more details, refer to the [Gallery Documentation]({{% relref "features/model-gallery" %}}).

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

The WebUI provides a powerful model import interface that supports both simple and advanced configuration:

### Simple Import Mode

1. Open the LocalAI WebUI at `http://localhost:8080`
2. Click "Import Model"
3. Enter the model URI (e.g., `https://huggingface.co/Qwen/Qwen3-VL-8B-Instruct-GGUF`)
4. Optionally configure preferences:
   - Backend selection
   - Model name
   - Description
   - Quantizations
   - Embeddings support
   - Custom preferences
5. Click "Import Model" to start the import process

### Advanced Import Mode

For full control over model configuration:

1. In the WebUI, click "Import Model"
2. Toggle to "Advanced Mode"
3. Edit the YAML configuration directly in the code editor
4. Use the "Validate" button to check your configuration
5. Click "Create" or "Update" to save

The advanced editor includes:
- Syntax highlighting
- YAML validation
- Format and copy tools
- Full configuration options

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

### Run Models via URI

To run models via URI, specify a URI to a model file or a configuration file when starting LocalAI. Valid syntax includes:

- `file://path/to/model`
- `huggingface://repository_id/model_file` (e.g., `huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf`)
- From OCIs: `oci://container_image:tag`, `ollama://model_id:tag`
- From configuration files: `https://gist.githubusercontent.com/.../phi-2.yaml`

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

docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:latest --models-path /models --context-size 700 --threads 4

curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.gguf",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

{{% notice tip %}}
**Other Docker Images**:

For other Docker images, please refer to the table in [the container images section]({{% relref "getting-started/container-images" %}}).
 {{% /notice %}}

### Example:

```bash
mkdir models

wget https://huggingface.co/TheBloke/Luna-AI-Llama2-Uncensored-GGUF/resolve/main/luna-ai-llama2-uncensored.Q4_0.gguf -O models/luna-ai-llama2

cp -rf prompt-templates/getting_started.tmpl models/luna-ai-llama2.tmpl

docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:latest --models-path /models --context-size 700 --threads 4

curl http://localhost:8080/v1/models

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "luna-ai-llama2",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9
   }'
```

{{% notice note %}}
- If running on Apple Silicon (ARM), it is **not** recommended to run on Docker due to emulation. Follow the [build instructions]({{% relref "installation/build" %}}) to use Metal acceleration for full GPU support.
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

For Kubernetes deployment, see the [Kubernetes installation guide]({{% relref "installation/kubernetes" %}}).

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

For instructions on building LocalAI from source, see the [Build from Source guide]({{% relref "installation/build" %}}).

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

```bash
# Via API
curl http://localhost:8080/v1/models

# Via CLI
local-ai models list
```

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
