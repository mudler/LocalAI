---
title: "Docker Installation"
description: "Install LocalAI using Docker containers - the recommended installation method"
weight: 1
url: '/installation/docker/'
---

{{% notice tip %}}
**Recommended Installation Method**

Docker is the recommended way to install LocalAI and provides the easiest setup experience.
{{% /notice %}}

LocalAI provides Docker images that work with Docker, Podman, and other container engines. These images are available on [Docker Hub](https://hub.docker.com/r/localai/localai) and [Quay.io](https://quay.io/repository/go-skynet/local-ai).

## Prerequisites

Before you begin, ensure you have Docker or Podman installed:

- [Install Docker Desktop](https://docs.docker.com/get-docker/) (Mac, Windows, Linux)
- [Install Podman](https://podman.io/getting-started/installation) (Linux alternative)
- [Install Docker Engine](https://docs.docker.com/engine/install/) (Linux servers)

## Quick Start

The fastest way to get started is with the CPU image:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

This will:
- Start LocalAI (you'll need to install models separately)
- Make the API available at `http://localhost:8080`

{{% notice tip %}}
**Docker Run vs Docker Start**

- `docker run` creates and starts a new container. If a container with the same name already exists, this command will fail.
- `docker start` starts an existing container that was previously created with `docker run`.

If you've already run LocalAI before and want to start it again, use: `docker start -i local-ai`
{{% /notice %}}

## Image Types

LocalAI provides several image types to suit different needs:

### Standard Images

Standard images don't include pre-configured models. Use these if you want to configure models manually.

#### CPU Image

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest
```

#### GPU Images

**NVIDIA CUDA 12:**
```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-gpu-nvidia-cuda-12
```

**NVIDIA CUDA 11:**
```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-gpu-nvidia-cuda-11
```

**AMD GPU (ROCm):**
```bash
docker run -ti --name local-ai -p 8080:8080 --device=/dev/kfd --device=/dev/dri --group-add=video localai/localai:latest-gpu-hipblas
```

**Intel GPU:**
```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-gpu-intel
```

**Vulkan:**
```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-gpu-vulkan
```

**NVIDIA Jetson (L4T ARM64):**
```bash
docker run -ti --name local-ai -p 8080:8080 --runtime nvidia --gpus all localai/localai:latest-nvidia-l4t-arm64
```

### All-in-One (AIO) Images

**Recommended for beginners** - These images come pre-configured with models and backends, ready to use immediately.

#### CPU Image

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-aio-cpu
```

#### GPU Images

**NVIDIA CUDA 12:**
```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-aio-gpu-nvidia-cuda-12
```

**NVIDIA CUDA 11:**
```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-aio-gpu-nvidia-cuda-11
```

**AMD GPU (ROCm):**
```bash
docker run -ti --name local-ai -p 8080:8080 --device=/dev/kfd --device=/dev/dri --group-add=video localai/localai:latest-aio-gpu-hipblas
```

**Intel GPU:**
```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-aio-gpu-intel
```

## Using Docker Compose

For a more manageable setup, especially with persistent volumes, use Docker Compose:

```yaml
version: "3.9"
services:
  api:
    image: localai/localai:latest-aio-cpu
    # For GPU support, use one of:
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
      - DEBUG=true
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

Save this as `docker-compose.yml` and run:

```bash
docker compose up -d
```

## Persistent Storage

To persist models and configurations, mount a volume:

```bash
docker run -ti --name local-ai -p 8080:8080 \
  -v $PWD/models:/models \
  localai/localai:latest-aio-cpu
```

Or use a named volume:

```bash
docker volume create localai-models
docker run -ti --name local-ai -p 8080:8080 \
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

## Advanced Configuration

For detailed information about:
- All available image tags and versions
- Advanced Docker configuration options
- Custom image builds
- Backend management

See the [Container Images documentation](/getting-started/container-images/).

## Troubleshooting

### Container won't start

- Check Docker is running: `docker ps`
- Check port 8080 is available: `netstat -an | grep 8080` (Linux/Mac)
- View logs: `docker logs local-ai`

### GPU not detected

- Ensure Docker has GPU access: `docker run --rm --gpus all nvidia/cuda:12.0.0-base-ubuntu22.04 nvidia-smi`
- For NVIDIA: Install [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html)
- For AMD: Ensure devices are accessible: `ls -la /dev/kfd /dev/dri`

### Models not downloading

- Check internet connection
- Verify disk space: `df -h`
- Check Docker logs for errors: `docker logs local-ai`

## See Also

- [Container Images Reference](/getting-started/container-images/) - Complete image reference
- [Install Models](/getting-started/models/) - Install and configure models
- [GPU Acceleration](/features/gpu-acceleration/) - GPU setup and optimization
- [Kubernetes Installation](/installation/kubernetes/) - Deploy on Kubernetes

