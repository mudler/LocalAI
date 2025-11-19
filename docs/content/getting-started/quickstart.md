+++
disableToc = false
title = "Quickstart"
weight = 3
url = '/basics/getting_started/'
icon = "rocket_launch"
+++

**LocalAI** is a free, open-source alternative to OpenAI (Anthropic, etc.), functioning as a drop-in replacement REST API for local inferencing. It allows you to run [LLMs]({{% relref "features/text-generation" %}}), generate images, and produce audio, all locally or on-premises with consumer-grade hardware, supporting multiple model families and architectures.

{{% notice tip %}}

**Security considerations**

If you are exposing LocalAI remotely, make sure you protect the API endpoints adequately with a mechanism which allows to protect from the incoming traffic or alternatively, run LocalAI with `API_KEY` to gate the access with an API key. The API key guarantees a total access to the features (there is no role separation), and it is to be considered as likely as an admin role.

 {{% /notice %}}

## Quickstart

### Using the Bash Installer

```bash
curl https://localai.io/install.sh | sh
```

The bash installer, if docker is not detected, will install automatically as a systemd service.

See [Installer]({{% relref "advanced/installer" %}}) for all the supported options

### macOS Download

For MacOS a DMG is available:

<a href="https://github.com/mudler/LocalAI/releases/latest/download/LocalAI.dmg">
  <img src="https://img.shields.io/badge/Download-macOS-blue?style=for-the-badge&logo=apple&logoColor=white" alt="Download LocalAI for macOS"/>
</a>

> Note: the DMGs are not signed by Apple and shows quarantined after install. See https://github.com/mudler/LocalAI/issues/6268 for a workaround, fix is tracked here: https://github.com/mudler/LocalAI/issues/6244

### Run with docker

{{% notice tip %}}
**Docker Run vs Docker Start**

- `docker run` creates and starts a new container. If a container with the same name already exists, this command will fail.
- `docker start` starts an existing container that was previously created with `docker run`.

If you've already run LocalAI before and want to start it again, use: `docker start -i local-ai`
 {{% /notice %}}

The following commands will automatically start with a web interface and a Rest API on port `8080`.

#### CPU only image:

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest
```

#### NVIDIA GPU Images:

```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-gpu-nvidia-cuda-12

docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-gpu-nvidia-cuda-11

docker run -ti --name local-ai -p 8080:8080 --runtime nvidia --gpus all localai/localai:latest-nvidia-l4t-arm64
```

#### AMD GPU Images (ROCm):

```bash
docker run -ti --name local-ai -p 8080:8080 --device=/dev/kfd --device=/dev/dri --group-add=video localai/localai:latest-gpu-hipblas
```

#### Intel GPU Images (oneAPI):

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-gpu-intel
```

#### Vulkan GPU Images:

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-gpu-vulkan
```

#### AIO Images (pre-downloaded models):

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-aio-cpu

docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-aio-gpu-nvidia-cuda-12

docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-aio-gpu-nvidia-cuda-11

docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-aio-gpu-intel

docker run -ti --name local-ai -p 8080:8080 --device=/dev/kfd --device=/dev/dri --group-add=video localai/localai:latest-aio-gpu-hipblas
```

### Downloading models on start

When starting LocalAI (either via Docker or via CLI) you can specify as argument a list of models to install automatically before starting the API, for example:

```bash
local-ai run llama-3.2-1b-instruct:q4_k_m
local-ai run huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf
local-ai run ollama://gemma:2b
local-ai run https://gist.githubusercontent.com/.../phi-2.yaml
local-ai run oci://localai/phi-2:latest
```

{{% notice tip %}}
**Automatic Backend Detection**: When you install models from the gallery or YAML files, LocalAI automatically detects your system's GPU capabilities (NVIDIA, AMD, Intel) and downloads the appropriate backend. For advanced configuration options, see [GPU Acceleration]({{% relref "features/gpu-acceleration#automatic-backend-detection" %}}).
 {{% /notice %}}

For a full list of options, you can run LocalAI with `--help` or refer to the [Installer Options]({{% relref "advanced/installer" %}}) documentation.

Binaries can also be [manually downloaded]({{% relref "reference/binaries" %}}).

## Using Homebrew on MacOS

{{% notice tip %}}
The Homebrew formula currently doesn't have the same options than the bash script
 {{% /notice %}}

You can install Homebrew's [LocalAI](https://formulae.brew.sh/formula/localai) with the following command:

```
brew install localai
```


## Using Container Images or Kubernetes

LocalAI is available as a container image compatible with various container engines such as Docker, Podman, and Kubernetes. Container images are published on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest) and [Docker Hub](https://hub.docker.com/r/localai/localai).

For detailed instructions, see [Using container images]({{% relref "getting-started/container-images" %}}). For Kubernetes deployment, see [Run with Kubernetes]({{% relref "getting-started/kubernetes" %}}).

## Running LocalAI with All-in-One (AIO) Images

> _Already have a model file? Skip to [Run models manually]({{% relref "getting-started/models" %}})_.

LocalAI's All-in-One (AIO) images are pre-configured with a set of models and backends to fully leverage almost all the features of LocalAI. If pre-configured models are not required, you can use the standard [images]({{% relref "getting-started/container-images" %}}).

These images are available for both CPU and GPU environments. AIO images are designed for ease of use and require no additional configuration.

It is recommended to use AIO images if you prefer not to configure the models manually or via the web interface. For running specific models, refer to the [manual method]({{% relref "getting-started/models" %}}).

The AIO images come pre-configured with the following features:
- Text to Speech (TTS)
- Speech to Text
- Function calling
- Large Language Models (LLM) for text generation
- Image generation
- Embedding server

For instructions on using AIO images, see [Using container images]({{% relref "getting-started/container-images#all-in-one-images" %}}).

## Using LocalAI and the full stack with LocalAGI

LocalAI is part of the Local family stack, along with LocalAGI and LocalRecall.

[LocalAGI](https://github.com/mudler/LocalAGI) is a powerful, self-hostable AI Agent platform designed for maximum privacy and flexibility which encompassess and uses all the software stack. It provides a complete drop-in replacement for OpenAI's Responses APIs with advanced agentic capabilities, working entirely locally on consumer-grade hardware (CPU and GPU).

### Quick Start

```bash
git clone https://github.com/mudler/LocalAGI
cd LocalAGI

docker compose up

docker compose -f docker-compose.nvidia.yaml up

docker compose -f docker-compose.intel.yaml up

MODEL_NAME=gemma-3-12b-it docker compose up

MODEL_NAME=gemma-3-12b-it \
MULTIMODAL_MODEL=minicpm-v-4_5 \
IMAGE_MODEL=flux.1-dev-ggml \
docker compose -f docker-compose.nvidia.yaml up
```

### Key Features

- **Privacy-Focused**: All processing happens locally, ensuring your data never leaves your machine
- **Flexible Deployment**: Supports CPU, NVIDIA GPU, and Intel GPU configurations
- **Multiple Model Support**: Compatible with various models from Hugging Face and other sources
- **Web Interface**: User-friendly chat interface for interacting with AI agents
- **Advanced Capabilities**: Supports multimodal models, image generation, and more
- **Docker Integration**: Easy deployment using Docker Compose

### Environment Variables

You can customize your LocalAGI setup using the following environment variables:

- `MODEL_NAME`: Specify the model to use (e.g., `gemma-3-12b-it`)
- `MULTIMODAL_MODEL`: Set a custom multimodal model
- `IMAGE_MODEL`: Configure an image generation model

For more advanced configuration and API documentation, visit the [LocalAGI GitHub repository](https://github.com/mudler/LocalAGI).

## What's Next?

There is much more to explore with LocalAI! You can run any model from Hugging Face, perform video generation, and also voice cloning. For a comprehensive overview, check out the [features]({{% relref "features" %}}) section.

Explore additional resources and community contributions:

- [Installer Options]({{% relref "advanced/installer" %}})
- [Run from Container images]({{% relref "getting-started/container-images" %}})
- [Examples to try from the CLI]({{% relref "getting-started/try-it-out" %}})
- [Build LocalAI and the container image]({{% relref "getting-started/build" %}})
- [Run models manually]({{% relref "getting-started/models" %}})
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)
