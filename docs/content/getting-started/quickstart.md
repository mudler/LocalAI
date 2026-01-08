+++
disableToc = false
title = "Quickstart"
weight = 1
url = '/basics/getting_started/'
icon = "rocket_launch"
+++

**LocalAI** is a free, open-source alternative to OpenAI (Anthropic, etc.), functioning as a drop-in replacement REST API for local inferencing. It allows you to run [LLMs]({{% relref "features/text-generation" %}}), generate images, and produce audio, all locally or on-premises with consumer-grade hardware, supporting multiple model families and architectures.

{{% notice tip %}}

**Security considerations**

If you are exposing LocalAI remotely, make sure you protect the API endpoints adequately with a mechanism which allows to protect from the incoming traffic or alternatively, run LocalAI with `API_KEY` to gate the access with an API key. The API key guarantees a total access to the features (there is no role separation), and it is to be considered as likely as an admin role.

 {{% /notice %}}

## Quickstart

This guide assumes you have already [installed LocalAI](/installation/). If you haven't installed it yet, see the [Installation guide](/installation/) first.

### Starting LocalAI

Once installed, start LocalAI. For Docker installations:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

The API will be available at `http://localhost:8080`.

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

For a full list of options, you can run LocalAI with `--help` or refer to the [Linux Installation guide]({{% relref "installation/linux" %}}) for installer configuration options.

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

- [Linux Installation Options]({{% relref "installation/linux" %}})
- [Run from Container images]({{% relref "getting-started/container-images" %}})
- [Examples to try from the CLI]({{% relref "getting-started/try-it-out" %}})
- [Build LocalAI from source]({{% relref "installation/build" %}})
- [Run models manually]({{% relref "getting-started/models" %}})
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)
