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

## Core Components

LocalAI is more than just a single tool - it's a complete ecosystem:

1. **[LocalAI Core](https://github.com/mudler/LocalAI)**
   - OpenAI-compatible API
   - Multiple model support (LLMs, image, audio)
   - Model Context Protocol (MCP) for agentic capabilities
   - No GPU required
   - Fast inference with native bindings
   - [Github repository](https://github.com/mudler/LocalAI)

2. **[LocalAGI](https://github.com/mudler/LocalAGI)**
   - Autonomous AI agents
   - No coding required
   - WebUI and REST API support
   - Extensible agent framework
   - [Github repository](https://github.com/mudler/LocalAGI)

3. **[LocalRecall](https://github.com/mudler/LocalRecall)**
   - Semantic search
   - Memory management
   - Vector database
   - Perfect for AI applications
   - [Github repository](https://github.com/mudler/LocalRecall)

## Getting Started

LocalAI can be installed in several ways. **Docker is the recommended installation method** for most users as it provides the easiest setup and works across all platforms.

### Recommended: Docker Installation

The quickest way to get started with LocalAI is using Docker:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

For complete installation instructions including Docker, macOS, Linux, Kubernetes, and building from source, see the [Installation guide](/installation/).

## Key Features

- **Text Generation**: Run various LLMs locally
- **Image Generation**: Create images with stable diffusion
- **Audio Processing**: Text-to-speech and speech-to-text
- **Vision API**: Image understanding and analysis
- **Embeddings**: Vector database support
- **Functions**: OpenAI-compatible function calling
- **MCP Support**: Model Context Protocol for agentic capabilities
- **P2P**: Distributed inference capabilities

## Community and Support

LocalAI is a community-driven project. You can:

- Join our [Discord community](https://discord.gg/uJAeKSAGDy)
- Check out our [GitHub repository](https://github.com/mudler/LocalAI)
- Contribute to the project
- Share your use cases and examples

## Next Steps

Ready to dive in? Here are some recommended next steps:

1. **[Install LocalAI](/installation/)** - Start with [Docker installation](/installation/docker/) (recommended) or choose another method
2. [Explore available models](https://models.localai.io)
3. [Model compatibility](/model-compatibility/)
4. [Try out examples](https://github.com/mudler/LocalAI-examples)
5. [Join the community](https://discord.gg/uJAeKSAGDy)
6. [Check the LocalAI Github repository](https://github.com/mudler/LocalAI)
7. [Check the LocalAGI Github repository](https://github.com/mudler/LocalAGI)


## License

LocalAI is MIT licensed, created and maintained by [Ettore Di Giacinto](https://github.com/mudler).
