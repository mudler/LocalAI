+++
title = "Overview"
weight = 1
toc = true
description = "What is LocalAI?"
tags = ["Beginners"]
categories = [""]
url = "/docs/overview"
author = "Ettore Di Giacinto"
icon = "info"
+++


LocalAI is your complete AI stack for running AI models locally. It's designed to be simple, efficient, and accessible, providing a drop-in replacement for OpenAI's API while keeping your data private and secure.

## Why LocalAI?

In today's AI landscape, privacy, control, and flexibility are paramount. LocalAI addresses these needs by:

- **Privacy First**: Your data never leaves your machine
- **Complete Control**: Run models on your terms, with your hardware
- **Open Source**: MIT licensed and community-driven
- **Flexible Deployment**: From laptops to servers, with or without GPUs
- **Extensible**: Add new models and features as needed

## What's Included

LocalAI is a single binary (or container) that gives you everything you need:

- **OpenAI-compatible API** — Drop-in replacement for OpenAI, Anthropic, and Open Responses APIs
- **Built-in Web Interface** — Chat, model management, agent creation, image generation, and system monitoring
- **AI Agents** — Create autonomous agents with MCP (Model Context Protocol) tool support, directly from the UI
- **Multiple Model Support** — LLMs, image generation, text-to-speech, speech-to-text, vision, embeddings, and more
- **GPU Acceleration** — Automatic detection and support for NVIDIA, AMD, Intel, and Vulkan GPUs
- **Distributed Mode** — Scale horizontally with worker nodes, P2P federation, and model sharding
- **No GPU Required** — Runs on CPU with consumer-grade hardware

LocalAI integrates [LocalAGI](https://github.com/mudler/LocalAGI) (agent platform) and [LocalRecall](https://github.com/mudler/LocalRecall) (semantic memory) as built-in libraries — no separate installation needed.

## Getting Started

LocalAI can be installed in several ways. **Docker is the recommended installation method** for most users as it provides the easiest setup and works across all platforms.

### Recommended: Docker Installation

The quickest way to get started with LocalAI is using Docker:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest-cpu
```

Then open **http://localhost:8080** to access the web interface, install models, and start chatting.

For GPU support, see the [Container images reference]({{% relref "getting-started/container-images" %}}) or the [Quickstart guide]({{% relref "getting-started/quickstart" %}}).

For complete installation instructions including Docker, macOS, Linux, Kubernetes, and building from source, see the [Installation guide](/installation/).

## Key Features

- **Text Generation**: Run various LLMs locally (llama.cpp, transformers, vLLM, and more)
- **Image Generation**: Create images with Stable Diffusion, Flux, and other models
- **Audio Processing**: Text-to-speech and speech-to-text
- **Vision API**: Image understanding and analysis
- **Embeddings**: Vector representations for search and retrieval
- **Function Calling**: OpenAI-compatible tool use
- **AI Agents**: Autonomous agents with MCP tool support
- **MCP Apps**: Interactive tool UIs in the web interface
- **P2P & Distributed**: Federated inference and model sharding across machines

## Community and Support

LocalAI is a community-driven project. You can:

- Join our [Discord community](https://discord.gg/uJAeKSAGDy)
- Check out our [GitHub repository](https://github.com/mudler/LocalAI)
- Contribute to the project
- Share your use cases and examples

## Next Steps

Ready to dive in? Here are some recommended next steps:

1. **[Install LocalAI](/installation/)** - Start with [Docker installation](/installation/docker/) (recommended) or choose another method
2. **[Quickstart guide]({{% relref "getting-started/quickstart" %}})** - Get up and running in minutes
3. [Explore available models](https://models.localai.io)
4. [Model compatibility](/model-compatibility/)
5. [Try out examples]({{% relref "getting-started/try-it-out" %}})
6. [Join the community](https://discord.gg/uJAeKSAGDy)

## License

LocalAI is MIT licensed, created and maintained by [Ettore Di Giacinto](https://github.com/mudler).
