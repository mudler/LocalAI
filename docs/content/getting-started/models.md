+++
disableToc = false
title = "Install and Run Models"
weight = 4
icon = "rocket_launch"
+++

To install models with LocalAI, you can:

- Browse the Model Gallery from the Web Interface and install models with a couple of clicks. For more details, refer to the [Gallery Documentation]({{% relref "features/model-gallery" %}}).
- Specify a model from the LocalAI gallery during startup, e.g., `local-ai run <model_gallery_name>`.
- Use a URI to specify a model file (e.g., `huggingface://...`, `oci://`, or `ollama://`) when starting LocalAI, e.g., `local-ai run huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf`.
- Specify a URL to a model configuration file when starting LocalAI, e.g., `local-ai run https://gist.githubusercontent.com/.../phi-2.yaml`.
- Manually install the models by copying the files into the models directory (`--models`).

## Run and Install Models via the Gallery

To run models available in the LocalAI gallery, you can use the WebUI or specify the model name when starting LocalAI. Models can be found in the gallery via the Web interface, the [model gallery](https://models.localai.io), or the CLI with: `local-ai models list`.

To install a model from the gallery, use the model name as the URI. For example, to run LocalAI with the Hermes model, execute:

```bash
local-ai run hermes-2-theta-llama-3-8b
```

To install only the model, use:

```bash
local-ai models install hermes-2-theta-llama-3-8b
```

Note: The galleries available in LocalAI can be customized to point to a different URL or a local directory. For more information on how to setup your own gallery, see the [Gallery Documentation]({{% relref "features/model-gallery" %}}).

## Run Models via URI

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

## Run Models Manually

Follow these steps to manually run models using LocalAI:

1. **Prepare Your Model and Configuration Files**:
   Ensure you have a model file and, if necessary, a configuration YAML file. Customize model defaults and settings with a configuration file. For advanced configurations, refer to the [Advanced Documentation]({{% relref "advanced" %}}).

2. **GPU Acceleration**:
   For instructions on GPU acceleration, visit the [GPU Acceleration]({{% relref "features/gpu-acceleration" %}}) page.

3. **Run LocalAI**:
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

For more model configurations, visit the [Examples Section](https://github.com/mudler/LocalAI-examples/tree/main/configurations).
