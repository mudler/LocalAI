+++
disableToc = false
title = "Quickstart"
description = "Install LocalAI, run a first model and send a first chat message in about five minutes."
icon = "rocket_launch"
weight = 30
aliases = ["/basics/getting_started/"]
keywords = ["quickstart", "install", "first model", "run", "getting started"]
devbuild = "install"
+++

{{< diagram "quickstart-journey" "Quickstart in four steps: install with the script, Docker or the macOS app; start LocalAI; pick a model; talk to it in the web UI or the API. Then build an agent, install add-ons or add machines." >}}



LocalAI is an open-source AI engine and platform. It speaks the OpenAI and Anthropic APIs, and it serves text, vision, image, video, music, speech and embedding models. It runs on your hardware, and it does not need a GPU. This page takes you from nothing to a first chat reply.

## Install and start LocalAI

On Linux, or on a Mac with Apple Silicon, run the install script, then start LocalAI:

```bash
curl -sSL https://localai.io/install.sh | sh
local-ai
# open http://localhost:8080 and install a model from there
```

On Windows, on an Intel Mac, or when you prefer a container, use Docker instead:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

On Linux or a Mac you can also download the [desktop app]({{% relref "/start/install/desktop" %}}): a DMG for macOS or an AppImage for Linux. It installs and starts the server for you. For all install methods, see the [install guide]({{% relref "/start/install" %}}).

You should see the log line `LocalAI is started and running` with the address `:8080`. The script installs the `local-ai` program and does not start it. The command `local-ai` starts the server, with no model. Open `http://localhost:8080`: on a fresh install the first-run guide recommends a model for your hardware and downloads it when you agree. To run a model directly, name it, for example `local-ai run gemma-4-e2b-it-qat-q4_0`, a small Google Gemma 4 model that runs on a CPU (a download of about 4.3 GB). The first run of a model takes longer, because it downloads the model.

For GPU acceleration in a container, use the image that matches your hardware:

| Hardware | Docker image |
|----------|-------------|
| CPU only | `localai/localai:latest` |
| NVIDIA, CUDA 12 | `localai/localai:latest-gpu-nvidia-cuda-12` |
| NVIDIA, CUDA 13 | `localai/localai:latest-gpu-nvidia-cuda-13` |
| AMD (ROCm) | `localai/localai:latest-gpu-hipblas` |
| Intel GPU | `localai/localai:latest-gpu-intel` |
| Vulkan | `localai/localai:latest-gpu-vulkan` |

For NVIDIA GPUs, add `--gpus all`. For AMD, Intel and Vulkan, add the `--device` flags. See [Container images]({{% relref "/start/install/containers" %}}) for the full reference.

## Chat in the web interface

Open **http://localhost:8080** in your browser. The web interface lets you:

- **Chat** with any installed model
- **Explore, install, and manage models** from the Models page
- **Generate images**, audio, and more
- **Create and manage AI agents** with MCP tool support
- **Monitor system resources** and loaded models
- **Configure settings** including GPU acceleration

To get your first chat working:

1. On a fresh install, accept the model that the first-run guide recommends. Or open **Models → Explore**, search for `gemma-4-e2b-it-qat-q4_0` and click **Install**. It is a small Google Gemma 4 model that runs on a CPU and reads images. Wait for the download to finish. You can use it again in [Build your first agent]({{% relref "/start/first-agent" %}}).
2. Open **Chats**, select the model from the model list, type a message and send it.

You should see a reply within a few seconds. On a CPU, the first reply can take longer while the model loads.

To correct an earlier prompt or response without running the model again, hover
over the saved message and select **Edit**. **Save** updates that conversation's
local history; **Cancel** discards the draft.

## Download models from the CLI

When starting LocalAI (either via Docker or via CLI) you can specify as argument a list of models to install automatically before starting the API, for example:

```bash
local-ai run gemma-4-e2b-it-qat-q4_0
local-ai run huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf
local-ai run ollama://gemma:2b
local-ai run https://gist.githubusercontent.com/.../phi-2.yaml
local-ai run oci://localai/phi-2:latest
```

You can also manage models with the CLI:

```bash
local-ai models list          # List available models in the gallery
local-ai models install <name> # Install a model
```

{{% callout style="tip" %}}
**Automatic Backend Detection**: When you install models from the gallery or YAML files, LocalAI automatically detects your system's GPU capabilities (NVIDIA, AMD, Intel) and downloads the appropriate backend. For advanced configuration options, see [GPU Acceleration]({{% relref "/models/gpu-acceleration#automatic-backend-detection" %}}).
 {{% /callout %}}

For a full list of options, run LocalAI with `--help`, or see the [Linux Installation guide]({{% relref "/start/install/linux" %}}).

## Use the API

LocalAI exposes an OpenAI-compatible API. You can use it with any OpenAI SDK or client by pointing it to `http://localhost:8080`. For example:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemma-4-e2b-it-qat-q4_0",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

You should see a JSON answer. The text of the reply is in `choices[0].message.content`.

LocalAI also supports the **Anthropic Messages API**, the **Open Responses API**, and more. See [Try it out]({{% relref "/start/try-it-out" %}}) for examples of all supported endpoints.

## Build an agent

{{< devbuild >}}

An agent is a saved set of instructions and tool grants. It is part of LocalAI. You need no extra install.

1. Install a model that supports tool calling, such as the one the first-run guide recommends.
2. Open **Agents** in the sidebar and choose **New agent**.
3. Give it a name and instructions, then choose **Create agent**.
4. Choose **Chat** on the agent's row.

For the full walkthrough, see [Build your first agent]({{% relref "/start/first-agent" %}}).

## Before you expose it

By default, LocalAI listens on port 8080 on all interfaces, and authentication is off. Protect the API before you expose it beyond your own machine. You have two options:

- **Simple API keys**: Run with `LOCALAI_API_KEY=your-key` to gate access. API keys grant full admin access with no role separation.
- **User authentication**: Run with `LOCALAI_AUTH=true` for multi-user support with admin and user roles, OAuth login, per-user API keys and usage tracking. See [Authentication and authorization]({{% relref "/administration/authentication" %}}).

## What next

There is much more to explore! LocalAI supports video generation, voice cloning, embeddings, image understanding, and more. Check out:

- [Container images reference]({{% relref "/start/install/containers" %}})
- [Try the API endpoints]({{% relref "/start/try-it-out" %}})
- [All features]({{% relref "/features" %}})
- [Model gallery](https://models.localai.io)
- [Run models manually]({{% relref "/models/setting-up-models" %}})
- [Build from source]({{% relref "/contributing/build-from-source" %}})
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)
