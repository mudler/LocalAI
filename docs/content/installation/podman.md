---
title: Podman
description: Install and use LocalAI with Podman
weight: 8
---

## Podman Installation

Install Podman on your system:

{{< tabs >}}
{{< tab "Ubuntu" >}}

```bash
sudo apt update
sudo apt install -y podman
```

{{< /tab >}}
{{< tab "Fedora" >}}

```bash
sudo dnf install -y podman
```

{{< /tab >}}
{{< tab "Arch Linux" >}}

```bash
sudo pacman -S podman
```

{{< /tab >}}
{{< tab "openSUSE" >}}

```bash
sudo zypper install podman
```

{{< /tab >}}
{{< tab "macOS" >}}

```bash
brew install podman
podman machine init
podman machine start
```

{{< /tab >}}
{{< tab "Windows (WSL2)" >}}

```bash
# Install Podman via winget or download from GitHub releases
winget install Podman.Podman
```

{{< /tab >}}
{{< /tabs >}}

## Quick Start

Run a LocalAI container with CPU support:

```bash
podman run -p 8080:8080 localai/localai:latest
```

## Image Variants

LocalAI provides multiple image variants for different hardware. For a complete list of available images, see the [Docker installation guide](docker/#image-types).

### NVIDIA GPU

```bash
podman run -d --device nvidia.com/gpu=all \
  -p 8080:8080 \
  localai/localai:latest-gpu-nvidia-cuda-12
```

### AMD GPU (ROCm)

```bash
podman run -d --device rocm.com/gpu=all \
  -p 8080:8080 \
  localai/localai:latest-gpu-hipblas
```

### Intel GPU

```bash
podman run -d --device gpu.intel.com/all \
  -p 8080:8080 \
  localai/localai:latest-gpu-intel
```

## Rootless Containers

Podman runs rootless by default, which is a key advantage over Docker. For GPU access in rootless mode:

```bash
# Ensure NVIDIA Container Toolkit is installed
# For rootless GPU access, you may need to set:
podman run --device nvidia.com/gpu=all \
  -e NVIDIA_VISIBLE_DEVICES=all \
  -e NVIDIA_DRIVER_CAPABILITIES=compute,utility,video \
  -p 8080:8080 \
  localai/localai:latest-gpu-nvidia-cuda-12
```

## Docker Compose

You can use `podman-compose` or Podman's native Kubernetes generation:

### Using podman-compose

```bash
# Install podman-compose
pip install podman-compose

# Create compose.yaml
cat > compose.yaml << 'EOF'
services:
  localai:
    image: localai/localai:latest
    ports:
      - "8080:8080"
    volumes:
      - ./models:/models
EOF

# Run
podman-compose up -d
```

### Using Podman Generate Kube

```bash
# Generate Kubernetes YAML from running container
podman generate kube localai > localai.yaml

# Or from an image
podman kube play localai.yaml
```

## Persistent Storage

Mount local directories for models and configuration:

```bash
podman run -d -p 8080:8080 \
  -v $(pwd)/models:/models:z \
  -v $(pwd)/config:/etc/localai:z \
  localai/localai:latest
```

## Health Checks

Check container health using Podman's native health check:

```bash
# Check container status
podman ps

# Inspect health status
podman inspect --format '{{.State.Health.Status}}' localai

# View health check logs
podman logs --healthcheck localai
```

## Troubleshooting

### GPU Not Detected

```bash
# Verify GPU is visible to Podman
podman info | grep nvidia

# Check NVIDIA devices
nvidia-smi
```

### Rootless GPU Issues

```bash
# For NVIDIA, ensure nvidia-container-toolkit is configured
sudo nvidia-container-toolkit configure
sudo systemctl restart podman
```

### Volume Permission Issues

```bash
# Use :z or :Z to relabel volume for shared access
podman run -v ./models:/models:z localai/localai:latest
```

For more information, see the [installation overview](../).
