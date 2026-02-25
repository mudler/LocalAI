---
title: Containers
description: Install and use LocalAI with container engines (Docker, Podman)
weight: 1
url: '/installation/containers/'
---

LocalAI supports Docker, Podman, and other OCI-compatible container engines. This guide covers the common aspects of running LocalAI in containers.

## Prerequisites

Before you begin, ensure you have a container engine installed:

- [Install Docker](https://docs.docker.com/get-docker/) (Mac, Windows, Linux)
- [Install Podman](https://podman.io/getting-started/installation) (Linux, macOS, Windows WSL2)

## Quick Start

The fastest way to get started is with the CPU image:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
# Or with Podman:
podman run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

This will:
- Start LocalAI (you'll need to install models separately)
- Make the API available at `http://localhost:8080`

## Image Types

LocalAI provides several image types to suit different needs. These images work with both Docker and Podman.

### Standard Images

Standard images don't include pre-configured models. Use these if you want to configure models manually.

#### CPU Image

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 localai/localai:latest
```

#### GPU Images

**NVIDIA CUDA 13:**
```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-gpu-nvidia-cuda-13
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device nvidia.com/gpu=all localai/localai:latest-gpu-nvidia-cuda-13
```

**NVIDIA CUDA 12:**
```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-gpu-nvidia-cuda-12
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device nvidia.com/gpu=all localai/localai:latest-gpu-nvidia-cuda-12
```

**AMD GPU (ROCm):**
```bash
docker run -ti --name local-ai -p 8080:8080 --device=/dev/kfd --device=/dev/dri --group-add=video localai/localai:latest-gpu-hipblas
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device rocm.com/gpu=all localai/localai:latest-gpu-hipblas
```

**Intel GPU:**
```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-gpu-intel
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device gpu.intel.com/all localai/localai:latest-gpu-intel
```

**Vulkan:**
```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-gpu-vulkan
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 localai/localai:latest-gpu-vulkan
```

**NVIDIA Jetson (L4T ARM64):**

CUDA 12 (for Nvidia AGX Orin and similar platforms):
```bash
docker run -ti --name local-ai -p 8080:8080 --runtime nvidia --gpus all localai/localai:latest-nvidia-l4t-arm64
```

CUDA 13 (for Nvidia DGX Spark):
```bash
docker run -ti --name local-ai -p 8080:8080 --runtime nvidia --gpus all localai/localai:latest-nvidia-l4t-arm64-cuda-13
```

### All-in-One (AIO) Images

**Recommended for beginners** - These images come pre-configured with models and backends, ready to use immediately.

#### CPU Image

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-aio-cpu
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 localai/localai:latest-aio-cpu
```

#### GPU Images

**NVIDIA CUDA 13:**
```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-aio-gpu-nvidia-cuda-13
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device nvidia.com/gpu=all localai/localai:latest-aio-gpu-nvidia-cuda-13
```

**NVIDIA CUDA 12:**
```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-aio-gpu-nvidia-cuda-12
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device nvidia.com/gpu=all localai/localai:latest-aio-gpu-nvidia-cuda-12
```

**AMD GPU (ROCm):**
```bash
docker run -ti --name local-ai -p 8080:8080 --device=/dev/kfd --device=/dev/dri --group-add=video localai/localai:latest-aio-gpu-hipblas
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device rocm.com/gpu=all localai/localai:latest-aio-gpu-hipblas
```

**Intel GPU:**
```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-aio-gpu-intel
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device gpu.intel.com/all localai/localai:latest-aio-gpu-intel
```

## Using Compose

For a more manageable setup, especially with persistent volumes, use Docker Compose or Podman Compose:

```yaml
version: "3.9"
services:
  api:
    image: localai/localai:latest-aio-cpu
    # For GPU support, use one of:
    # image: localai/localai:latest-aio-gpu-nvidia-cuda-13
    # image: localai/localai:latest-aio-gpu-nvidia-cuda-12
    # image: localai/localai:latest-aio-gpu-nvidia-cuda-11
    # image: localai/localai:latest-aio-gpu-hipblas
    # image: localai/localai:latest-aio-gpu-intel
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/readyz"]
      interval: 1m
      timeout: 20m
      retries: 5
    ports:
      - 8080:8080
    environment:
      - DEBUG=false
    volumes:
      - ./models:/models:cached
    # For NVIDIA GPUs, uncomment:
    # deploy:
    #   resources:
    #     reservations:
    #       devices:
    #         - driver: nvidia
    #           count: 1
    #           capabilities: [gpu]
```

Save this as `compose.yaml` and run:

```bash
docker compose up -d
# Or with Podman:
podman-compose up -d
```

## Persistent Storage

To persist models and configurations, mount a volume:

```bash
docker run -ti --name local-ai -p 8080:8080 \
  -v $PWD/models:/models \
  localai/localai:latest-aio-cpu
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 \
  -v $PWD/models:/models \
  localai/localai:latest-aio-cpu
```

Or use a named volume:

```bash
docker volume create localai-models
docker run -ti --name local-ai -p 8080:8080 \
  -v localai-models:/models \
  localai/localai:latest-aio-cpu
# Or with Podman:
podman volume create localai-models
podman run -ti --name local-ai -p 8080:8080 \
  -v localai-models:/models \
  localai/localai:latest-aio-cpu
```

## What's Included in AIO Images

All-in-One images come pre-configured with:

- **Text Generation**: LLM models for chat and completion
- **Image Generation**: Stable Diffusion models
- **Text to Speech**: TTS models
- **Speech to Text**: Whisper models
- **Embeddings**: Vector embedding models
- **Function Calling**: Support for OpenAI-compatible function calling

The AIO images use OpenAI-compatible model names (like `gpt-4`, `gpt-4-vision-preview`) but are backed by open-source models. See the [container images documentation](/getting-started/container-images/#all-in-one-images) for the complete mapping.

## Next Steps

After installation:

1. Access the WebUI at `http://localhost:8080`
2. Check available models: `curl http://localhost:8080/v1/models`
3. [Install additional models](/getting-started/models/)
4. [Try out examples](/getting-started/try-it-out/)

## Troubleshooting

### Container won't start

- Check container engine is running: `docker ps` or `podman ps`
- Check port 8080 is available: `netstat -an | grep 8080` (Linux/Mac)
- View logs: `docker logs local-ai` or `podman logs local-ai`

### GPU not detected

- Ensure Docker has GPU access: `docker run --rm --gpus all nvidia/cuda:12.0.0-base-ubuntu22.04 nvidia-smi`
- For Podman, see the [Podman installation guide](/installation/podman/#gpu-not-detected)
- For NVIDIA: Install [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html)
- For AMD: Ensure devices are accessible: `ls -la /dev/kfd /dev/dri`

### Models not downloading

- Check internet connection
- Verify disk space: `df -h`
- Check container logs for errors: `docker logs local-ai` or `podman logs local-ai`

## See Also

- [Container Images Reference](/getting-started/container-images/) - Complete image reference
- [Install Models](/getting-started/models/) - Install and configure models
- [GPU Acceleration](/features/gpu-acceleration/) - GPU setup and optimization
- [Kubernetes Installation](/installation/kubernetes/) - Deploy on Kubernetes
