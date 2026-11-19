+++
disableToc = false
title = "Quickstart"
description = "Install LocalAI, run a first model and send a first chat message in about five minutes."
icon = "rocket_launch"
weight = 30
aliases = ["/basics/getting_started/"]
keywords = ["quickstart", "install", "first model", "run", "getting started"]
+++

{{< diagram "quickstart-journey" "Quickstart in four steps: install with the script, Docker or the desktop app; start LocalAI; pick a model; talk to it in the web UI or the API. Then build an agent, install add-ons or add machines." >}}

Run open models for chat, images, speech and video on your own machines. The API speaks the OpenAI and Anthropic formats. Your team gets chats, files and documents on top, and the assistant can write and run code. No GPU needed for the engine, and a GPU makes it faster.

## Install and start LocalAI

On Linux, or on a Mac with Apple Silicon, run the install script, then start LocalAI:

```bash
curl -sSL https://localai.io/install.sh | sh
local-ai
# open http://localhost:8080 and install a model from there
```

The script installs the `local-ai` program and does not start it. The command `local-ai` starts the server with no model. You should see the log line `LocalAI is started and running` with the address `:8080`. If the script says the folder is not on your `PATH`, run the `export` line it prints.

On Windows, use the PowerShell script `irm https://localai.io/install.ps1 | iex`. See [Install on Windows]({{% relref "/start/install/windows" %}}).

On an Intel Mac, or when you prefer a container, use Docker instead:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

A desktop app for macOS and Linux is in the 5.0 release. See the [install guide]({{% relref "/start/install" %}}) for the other ways to install.

For a GPU in a container, use the image that matches your hardware:

| Hardware | Docker image |
|----------|-------------|
| CPU only | `localai/localai:latest` |
| NVIDIA, CUDA 12 | `localai/localai:latest-gpu-nvidia-cuda-12` |
| NVIDIA, CUDA 13 | `localai/localai:latest-gpu-nvidia-cuda-13` |
| AMD (ROCm) | `localai/localai:latest-gpu-hipblas` |
| Intel GPU | `localai/localai:latest-gpu-intel` |
| Vulkan | `localai/localai:latest-gpu-vulkan` |

For NVIDIA GPUs, add `--gpus all`. For AMD, Intel and Vulkan, add the `--device` flags. NVIDIA Jetson images and the full flag list are in [Container images]({{% relref "/start/install/containers" %}}).

## Chat in the web interface

Open **http://localhost:8080** in your browser.

1. On a fresh install, accept the model that the first-run card recommends. LocalAI downloads it when you agree. Or open **Models**, then **Explore**, search for `gemma-4-e2b-it-qat-q4_0` and choose **Install**. It's a small Google Gemma 4 model that runs on a CPU and reads images. The download is 4.3 GB in total: 3.35 GB for the model and 0.99 GB for the image projector. The first-run card shows 3.1 GB because it counts the model file only, in binary units.
2. Open **Chats**, select the model from the model list, type a message and send it.

On a CPU the first reply takes longer because the model has to load.

## Use the API

Any OpenAI SDK or client works when you point it at `http://localhost:8080`. This request uses the model you just installed:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemma-4-e2b-it-qat-q4_0",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

The text of the reply is in `choices[0].message.content`. If you turned on API keys (see below), add `-H "Authorization: Bearer <your-key>"` to the request.

LocalAI also speaks the Anthropic Messages API and the Open Responses API. [Try it out]({{% relref "/start/try-it-out" %}}) has an example for each endpoint.

## Install models from the command line

Name a model after `run` and LocalAI downloads it before it starts the API:

```bash
local-ai run gemma-4-e2b-it-qat-q4_0
local-ai run llama-3.2-1b-instruct:q4_k_m
local-ai run huggingface://google/gemma-4-E2B-it-qat-q4_0-gguf/gemma-4-E2B_q4_0-it.gguf
```

The first two names come from the model gallery. The third one points at a file on Hugging Face. To list the gallery or install a model without starting the server:

```bash
local-ai models list
local-ai models install <name>
```

`ollama://`, `oci://` and a URL of a model configuration file work the same way. [Set up models]({{% relref "/models/setting-up-models" %}}) lists every form. LocalAI checks your GPU when you install a model and downloads the matching backend ([GPU acceleration]({{% relref "/models/gpu-acceleration#automatic-backend-detection" %}})). For all options, see the [CLI reference]({{% relref "/reference/cli-reference" %}}) or run `local-ai --help`.

## Build an agent

{{< devbuild >}}

An agent is a chat with fixed instructions and a fixed set of tools and files. It's part of LocalAI, so you need no extra install. On a server with accounts, an administrator switches saved agents on first.

1. Install a model that supports tool calling. The gallery entry of `gemma-4-e2b-it-qat-q4_0` turns on automatic tool-call parsing, so it's a good first try. If the agent never calls a tool, switch to a larger model.
2. Open **Agents** in the sidebar and choose **New agent**.
3. Give it a name and instructions, then choose **Create agent**.
4. Choose **Chat** on the agent's row.

[Build your first agent]({{% relref "/start/first-agent" %}}) has the full walkthrough.

## Before you expose it

LocalAI listens on port 8080 on all interfaces, and authentication is off. Protect the API before you expose it beyond your own machine:

- **API keys.** Run with `LOCALAI_API_KEY=your-key`. API keys give full admin access with no role separation.
- **User authentication.** Run with `LOCALAI_AUTH=true` for admin and user roles, OAuth login, per-user API keys and usage tracking. See [Authentication and authorization]({{% relref "/administration/authentication" %}}).

## What next

- [Container images]({{% relref "/start/install/containers" %}})
- [Try the API endpoints]({{% relref "/start/try-it-out" %}})
- [All features]({{% relref "/features" %}})
- [Model gallery]({{% relref "/models" %}})
- [Build from source]({{% relref "/contributing/build-from-source" %}})
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)
