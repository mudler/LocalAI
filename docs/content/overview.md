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


LocalAI is a composable AI stack for running models locally: a small core that speaks the OpenAI and Anthropic APIs, with each model backend added only when you need it. It's simple, efficient, and private by default, and a drop-in replacement that keeps your data on your own hardware.

![How LocalAI works: clients speak one API to a small core, which routes each request over gRPC to separate backend processes pulled on demand](/images/diagrams/architecture-overview.png)

## Why LocalAI?

In today's AI landscape, privacy, control, and flexibility are paramount. LocalAI addresses these needs by:

- **Privacy First**: Your data never leaves your machine
- **Complete Control**: Run models on your terms, with your hardware
- **Open Source**: MIT licensed and community-driven
- **Flexible Deployment**: From laptops to servers, with or without GPUs
- **Composable by design**: A small core, not a bundle. Backends are separate and installed on demand, so you only run what you use

## What's Included

The LocalAI core is a single small binary (or container). It gives you everything you need to serve models, and pulls each model backend on demand, so you install only what you use:

- **OpenAI-compatible API** — Drop-in replacement for OpenAI, Anthropic, and Open Responses APIs
- **Built-in Web Interface** — Chat, model management, agent creation, image generation, and system monitoring
- **AI Agents** — Create autonomous agents with MCP (Model Context Protocol) tool support, directly from the UI
- **Any Model, Any Modality**: LLMs, image and video, text-to-speech, speech-to-text, vision, and embeddings, each on its own backend, pulled automatically when you load a model
- **GPU Acceleration** — Automatic detection and support for NVIDIA, AMD, Intel, and Vulkan GPUs
- **Distributed Mode** — Scale horizontally with worker nodes, P2P federation, and model sharding
- **No GPU Required** — Runs on CPU with consumer-grade hardware

LocalAI integrates [LocalAGI](https://github.com/mudler/LocalAGI) (agent platform) and [LocalRecall](https://github.com/mudler/LocalRecall) (semantic memory) as built-in libraries — no separate installation needed.

Each backend is a dedicated gRPC service that LocalAI builds around a best-in-class engine (llama.cpp, vLLM, whisper.cpp, stable-diffusion, MLX, and more), exposing it through the unified API. Backends ship as standard OCI images and run as isolated processes, so each one can be installed, upgraded, or removed without touching the core, can even run on a separate machine, and a fault in one never brings down the rest.

Because the backend contract is a simple gRPC interface, the system is open: bring your own model, or write a custom backend in any language and plug it in, exactly how the built-in backends work. This is what keeps the core small and gives you the flexibility to run precisely the stack you want, instead of compiling every engine into one binary.

## Getting Started

LocalAI can be installed in several ways. **Docker is the recommended installation method** for most users as it provides the easiest setup and works across all platforms.

### Recommended: Docker Installation

The quickest way to get started with LocalAI is using Docker:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

Then open **http://localhost:8080** to access the web interface, install models, and start chatting.

For GPU support, see the [Container images reference]({{% relref "installation/containers" %}}) or the [Quickstart guide]({{% relref "getting-started/quickstart" %}}).

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

## Team

LocalAI is created by [Ettore Di Giacinto](https://github.com/mudler) and maintained by the LocalAI team:

- **[Ettore Di Giacinto](https://github.com/mudler)** — original author and project lead
- **[Richard Palethorpe](https://github.com/richiejp)** — maintainer

LocalAI is helped by the wider community of contributors. See the full [contributors list](https://github.com/mudler/LocalAI/graphs/contributors).

## License

LocalAI is MIT licensed.
