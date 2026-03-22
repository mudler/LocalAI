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

## Using Compose

For a more manageable setup, especially with persistent volumes, use Docker Compose or Podman Compose:

### Using CDI (Container Device Interface) - Recommended for NVIDIA Container Toolkit 1.14+

The CDI approach is recommended for newer versions of the NVIDIA Container Toolkit (1.14 and later). It provides better compatibility and is the future-proof method:

```yaml
version: "3.9"
services:
  api:
    image: localai/localai:latest-gpu-nvidia-cuda-12
    # For CUDA 13, use: localai/localai:latest-gpu-nvidia-cuda-13
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
    # CDI driver configuration (recommended for NVIDIA Container Toolkit 1.14+)
    # This uses the nvidia.com/gpu resource API
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia.com/gpu
              count: all
              capabilities: [gpu]
```

Save this as `compose.yaml` and run:

```bash
docker compose up -d
# Or with Podman:
podman-compose up -d
```

### Using Legacy NVIDIA Driver - For Older NVIDIA Container Toolkit

If you are using an older version of the NVIDIA Container Toolkit (before 1.14), or need backward compatibility, use the legacy approach:

```yaml
version: "3.9"
services:
  api:
    image: localai/localai:latest-gpu-nvidia-cuda-12
    # For CUDA 13, use: localai/localai:latest-gpu-nvidia-cuda-13
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
    # Legacy NVIDIA driver configuration (for older NVIDIA Container Toolkit)
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]
```

## Persistent Storage

The container exposes the following volumes:

| Volume | Description | CLI Flag | Environment Variable |
|--------|-------------|----------|----------------------|
| `/models` | Model files used for inferencing | `--models-path` | `$LOCALAI_MODELS_PATH` |
| `/backends` | Custom backends for inferencing | `--backends-path` | `$LOCALAI_BACKENDS_PATH` |
| `/configuration` | Dynamic config files (api_keys.json, external_backends.json, runtime_settings.json) | `--localai-config-dir` | `$LOCALAI_CONFIG_DIR` |
| `/data` | Persistent data (collections, agent state, tasks, jobs) | `--data-path` | `$LOCALAI_DATA_PATH` |

To persist models and data, mount volumes:

```bash
docker run -ti --name local-ai -p 8080:8080 \
  -v $PWD/models:/models \
  -v $PWD/data:/data \
  localai/localai:latest
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 \
  -v $PWD/models:/models \
  -v $PWD/data:/data \
  localai/localai:latest
```

Or use named volumes:

```bash
docker volume create localai-models
docker volume create localai-data
docker run -ti --name local-ai -p 8080:8080 \
  -v localai-models:/models \
  -v localai-data:/data \
  localai/localai:latest
# Or with Podman:
podman volume create localai-models
podman volume create localai-data
podman run -ti --name local-ai -p 8080:8080 \
  -v localai-models:/models \
  -v localai-data:/data \
  localai/localai:latest
```

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

### NVIDIA Container fails to start with "Auto-detected mode as 'legacy'" error

If you encounter this error:
```
Error response from daemon: failed to create task for container: failed to create shim task: OCI runtime create failed: runc create failed: unable to start container process: error during container init: error running prestart hook #0: exit status 1, stdout: , stderr: Auto-detected mode as 'legacy'
nvidia-container-cli: requirement error: invalid expression
```

This indicates a Docker/NVIDIA Container Toolkit configuration issue. The container runtime's prestart hook fails before LocalAI starts. This is **not** a LocalAI code bug.

**Solutions:**

1. **Use CDI mode (recommended)**: Update your docker-compose.yaml to use the CDI driver configuration:
   ```yaml
   deploy:
     resources:
       reservations:
         devices:
           - driver: nvidia.com/gpu
             count: all
             capabilities: [gpu]
   ```

2. **Upgrade NVIDIA Container Toolkit**: Ensure you have version 1.14 or later, which has better CDI support.

3. **Check NVIDIA Container Toolkit configuration**: Run `nvidia-container-cli --query-gpu` to verify your installation is working correctly outside of containers.

4. **Verify Docker GPU access**: Test with `docker run --rm --gpus all nvidia/cuda:12.0.0-base-ubuntu22.04 nvidia-smi`

### Models not downloading

- Check internet connection
- Verify disk space: `df -h`
- Check container logs for errors: `docker logs local-ai` or `podman logs local-ai`

## See Also

- [Container Images Reference](/getting-started/container-images/) - Complete image reference
- [Install Models](/getting-started/models/) - Install and configure models
- [GPU Acceleration](/features/gpu-acceleration/) - GPU setup and optimization
- [Kubernetes Installation](/installation/kubernetes/) - Deploy on Kubernetes
