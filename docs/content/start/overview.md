+++
title = "Overview"
toc = true
description = "What LocalAI does and how it is built."
tags = ["Beginners"]
categories = [""]
author = "Ettore Di Giacinto"
icon = "info"
weight = 20
aliases = ["/overview/"]
devbuild = "install"
+++

Run open models for chat, images, speech and video on your own machines. The API speaks the OpenAI and Anthropic formats. Your team gets chats, files and documents on top, and the assistant can write and run code.

## What you can do

- **Call an API.** Routes in the OpenAI, Anthropic and Open Responses formats. A client that uses the OpenAI or Anthropic SDK works when you change the base URL.
- **Use the web interface.** Chats, saved agents, files, model management, image generation and system monitoring.
- **Build agents.** Saved agents use tools over MCP (Model Context Protocol), add-ons and your files.
- **Run many kinds of models.** Text, images, video, speech, sound, vision and embeddings. See [what models can do]({{% relref "/models/capabilities" %}}).
- **Use your GPU, or none.** LocalAI runs on NVIDIA, AMD, Intel and Vulkan-capable GPUs, and on a CPU.
- **Run several nodes.** Worker nodes and P2P federation, and P2P model sharding for llama.cpp models only. See [Distributed mode]({{% relref "/administration/distributed-mode" %}}).
- **Add programs.** Add-ons are separate programs that add tools, such as the Sandbox and Office documents.

Your files and models stay on your machines. Features that call outside services, such as web search or Gmail, are opt-in.

## Install

The [quickstart]({{% relref "/start/quickstart" %}}) has the install script, the first model and the first chat. The script works on Linux and on Macs with Apple Silicon. Windows has its own PowerShell script and a native binary, see [Install on Windows]({{% relref "/start/install/windows" %}}). For an Intel Mac or a GPU image, use a container:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

Files inside the container are lost when you remove it. See [Persistent storage]({{% relref "/start/install/containers#persistent-storage" %}}) for the four paths to mount. All install methods are in the [install guide]({{% relref "/start/install" %}}).

## How it works

{{< diagram "architecture-overview" "How LocalAI works: clients call one API on a small core with chats, agents, an add-on host and an MCP client and server, which talks over gRPC to a separate backend process for each model, running on your own hardware." >}}

The core serves the API and the web interface. Each model runs in a backend, which is a gRPC service around an engine such as llama.cpp, vLLM, whisper.cpp, stable-diffusion or MLX. Backends are OCI images that run as separate processes. LocalAI downloads a backend the first time a model needs it. You can install, upgrade or remove one without touching the core, and one can run on another machine.

The backend contract is a small gRPC interface, so you can write your own backend in any language. See [Creating a backend]({{% relref "/models/backends#creating-a-backend" %}}) and the [step-by-step checklist](https://github.com/mudler/LocalAI/blob/master/.agents/adding-backends.md).

## Next steps

- [Quickstart]({{% relref "/start/quickstart" %}}): run a first model and send a first message.
- [Model gallery]({{% relref "/models" %}}) and the [compatibility table]({{% relref "/models/compatibility-table" %}}).
- [Try out examples]({{% relref "/start/try-it-out" %}}).
- [Discord community](https://discord.gg/uJAeKSAGDy) and the [GitHub repository](https://github.com/mudler/LocalAI).

## License

LocalAI is open source under the MIT license. It was created by [Ettore Di Giacinto](https://github.com/mudler) and has many [contributors](https://github.com/mudler/LocalAI/graphs/contributors).
