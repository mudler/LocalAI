+++
disableToc = false
title = "Quickstart"
weight = 1
url = '/basics/getting_started/'
icon = "rocket_launch"
+++

**LocalAI** is a free, open-source alternative to OpenAI (Anthropic, etc.), functioning as a drop-in replacement REST API for local inferencing. It allows you to run [LLMs]({{% relref "features/text-generation" %}}), generate images, and produce audio, all locally or on-premises with consumer-grade hardware, supporting multiple model families and architectures.

LocalAI comes with a **built-in web interface** for chatting with models, managing installations, configuring AI agents, and more — no extra tools needed.

{{% notice tip %}}

**Security considerations**

If you are exposing LocalAI remotely, make sure you protect the API endpoints adequately. You have two options:

- **Simple API keys**: Run with `LOCALAI_API_KEY=your-key` to gate access. API keys grant full admin access with no role separation.
- **User authentication**: Run with `LOCALAI_AUTH=true` for multi-user support with admin/user roles, OAuth login, per-user API keys, and usage tracking. See [Authentication & Authorization]({{%relref "features/authentication" %}}) for details.

 {{% /notice %}}

## Quickstart

This guide assumes you have already [installed LocalAI](/installation/). If you haven't installed it yet, see the [Installation guide](/installation/) first.

### Starting LocalAI

Once installed, start LocalAI. For Docker installations:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest-cpu
```

For GPU acceleration, choose the image that matches your hardware:

| Hardware | Docker image |
|----------|-------------|
| CPU only | `localai/localai:latest-cpu` |
| NVIDIA CUDA | `localai/localai:latest-gpu-nvidia-cuda-12` |
| AMD (ROCm) | `localai/localai:latest-gpu-hipblas` |
| Intel GPU | `localai/localai:latest-gpu-intel` |
| Vulkan | `localai/localai:latest-gpu-vulkan` |

For NVIDIA GPUs, add `--gpus all`. For AMD/Intel/Vulkan, add the appropriate `--device` flags. See [Container images]({{% relref "getting-started/container-images" %}}) for the full reference.

### Using the Web Interface

Open **http://localhost:8080** in your browser. The web interface lets you:

- **Chat** with any installed model
- **Install models** from the built-in gallery (Models page)
- **Generate images**, audio, and more
- **Create and manage AI agents** with MCP tool support
- **Monitor system resources** and loaded models
- **Configure settings** including GPU acceleration

To get started, navigate to the **Models** page, browse the gallery, and install a model. Once installed, head to the **Chat** page to start a conversation.

### Downloading models from the CLI

When starting LocalAI (either via Docker or via CLI) you can specify as argument a list of models to install automatically before starting the API, for example:

```bash
local-ai run llama-3.2-1b-instruct:q4_k_m
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

{{% notice tip %}}
**Automatic Backend Detection**: When you install models from the gallery or YAML files, LocalAI automatically detects your system's GPU capabilities (NVIDIA, AMD, Intel) and downloads the appropriate backend. For advanced configuration options, see [GPU Acceleration]({{% relref "features/gpu-acceleration#automatic-backend-detection" %}}).
 {{% /notice %}}

For a full list of options, you can run LocalAI with `--help` or refer to the [Linux Installation guide]({{% relref "installation/linux" %}}) for installer configuration options.

### Using the API

LocalAI exposes an OpenAI-compatible API. You can use it with any OpenAI SDK or client by pointing it to `http://localhost:8080`. For example:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama-3.2-1b-instruct:q4_k_m",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

LocalAI also supports the **Anthropic Messages API**, the **Open Responses API**, and more. See [Try it out]({{% relref "getting-started/try-it-out" %}}) for examples of all supported endpoints.

## Built-in AI Agents

LocalAI includes a built-in AI agent platform with support for the [Model Context Protocol (MCP)](https://modelcontextprotocol.io/). You can create agents that use tools, browse the web, execute code, and interact with external services — all from the web interface.

To get started with agents:

1. Install a model that supports tool calling (most modern LLMs do)
2. Navigate to the **Agents** page in the web interface
3. Create a new agent, configure its tools and system prompt
4. Start chatting — the agent will use tools autonomously

No separate installation required — agents are part of LocalAI.

## Scaling with Distributed Mode

For production deployments or when you need more compute, LocalAI supports distributed mode with horizontal scaling:

- **Distributed nodes**: Add GPU worker nodes that self-register with a frontend coordinator
- **P2P federation**: Connect multiple LocalAI instances for load-balanced inference
- **Model sharding**: Split large models across multiple machines

See the **Nodes** page in the web interface or the [Distribution docs]({{% relref "features/distribution" %}}) for setup instructions.

## What's Next?

There is much more to explore! LocalAI supports video generation, voice cloning, embeddings, image understanding, and more. Check out:

- [Container images reference]({{% relref "getting-started/container-images" %}})
- [Try the API endpoints]({{% relref "getting-started/try-it-out" %}})
- [All features]({{% relref "features" %}})
- [Model gallery](https://models.localai.io)
- [Run models manually]({{% relref "getting-started/models" %}})
- [Build from source]({{% relref "installation/build" %}})
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)
