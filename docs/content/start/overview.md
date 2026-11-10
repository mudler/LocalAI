+++
title = "Overview"
toc = true
description = "What is LocalAI?"
tags = ["Beginners"]
categories = [""]
author = "Ettore Di Giacinto"
icon = "info"
weight = 20
aliases = ["/overview/"]
+++


LocalAI is an AI server that runs on your own hardware. A small core speaks the OpenAI and Anthropic APIs. Each model backend is added only when you need it. Many existing clients work with it.

{{< diagram "architecture-overview" "How LocalAI works: clients call one API on a small core with chats, agents, an add-on host and an MCP client and server, which talks over gRPC to a separate backend process for each model, running on your own hardware." >}}

## Why LocalAI

- **Self-hosted.** The server, the models and your files stay on your machines. Features that call outside services, such as web search or Gmail, are opt-in.
- **Open source.** MIT licence.
- **Any hardware.** Laptops and servers, with or without a GPU.
- **Small core.** Backends are separate. LocalAI installs a backend when a model needs it.

## What is included

- **OpenAI-compatible API.** Routes in the OpenAI, Anthropic and Open Responses formats.
- **Web interface.** Chats, saved agents, files, model management, image generation and system monitoring.
- **Agents.** Saved agents that use tools over MCP (Model Context Protocol), add-ons and your files.
- **Many modalities.** Text, images, video, speech, sound, vision and embeddings. Each one runs on its own backend.
- **GPU support.** LocalAI detects NVIDIA, AMD, Intel and Vulkan GPUs.
- **Distributed mode.** Worker nodes, P2P federation and model sharding.
- **Add-ons.** Separate programs that add tools, such as the Sandbox and Office documents.

Each backend is a gRPC service around an engine such as llama.cpp, vLLM, whisper.cpp, stable-diffusion or MLX. Backends are OCI images that run as separate processes. You can install, upgrade or remove one without touching the core. One can run on another machine. A fault in one does not stop the others.

The backend contract is a small gRPC interface. You can write your own backend in any language.

## Get started

On Linux or macOS, install with the script:

```bash
curl -sSL https://localai.io/install.sh | sh
local-ai run qwen3-4b
```

Then open **http://localhost:8080** to use the web interface. On Windows, or if you prefer a container, use Docker:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

For all methods, see the [install guide]({{% relref "/start/install" %}}). For GPU images, see the [container images reference]({{% relref "/start/install/containers" %}}).

## Community and Support

LocalAI is a community-driven project. You can:

- Join the [Discord community](https://discord.gg/uJAeKSAGDy)
- Visit the [GitHub repository](https://github.com/mudler/LocalAI)
- Contribute to the project
- Share your use cases and examples

## Next Steps

Ready to dive in? Here are some recommended next steps:

1. **[Install LocalAI]({{% relref "/start/install" %}})** - Start with the install script, or choose another method
2. **[Quickstart guide]({{% relref "/start/quickstart" %}})** - Run a first model and send a first message
3. [Explore available models](https://models.localai.io)
4. [Model compatibility]({{% relref "/models/compatibility-table" %}})
5. [Try out examples]({{% relref "/start/try-it-out" %}})
6. [Join the community](https://discord.gg/uJAeKSAGDy)

## Team

LocalAI is created by [Ettore Di Giacinto](https://github.com/mudler) and maintained by the LocalAI team:

- **[Ettore Di Giacinto](https://github.com/mudler)** - original author and project lead
- **[Richard Palethorpe](https://github.com/richiejp)** - maintainer

LocalAI is helped by the wider community of contributors. See the full [contributors list](https://github.com/mudler/LocalAI/graphs/contributors).

## License

LocalAI is MIT licensed.
