+++
disableToc = false
title = "Using GPU Acceleration"
weight = 3
icon = "memory"
description = "Set up GPU acceleration for faster inference"
+++

This tutorial will guide you through setting up GPU acceleration for LocalAI. GPU acceleration can significantly speed up model inference, especially for larger models.

## Prerequisites

- A compatible GPU (NVIDIA, AMD, Intel, or Apple Silicon)
- LocalAI installed
- Basic understanding of your system's GPU setup

## Check Your GPU

First, verify you have a compatible GPU:

### NVIDIA

```bash
nvidia-smi
```

You should see your GPU information. Ensure you have CUDA 11.7 or 12.0+ installed.

### AMD

```bash
rocminfo
```

### Intel

```bash
intel_gpu_top  # if available
```

### Apple Silicon (macOS)

Apple Silicon (M1/M2/M3) GPUs are automatically detected. No additional setup needed!

## Installation Methods

### Method 1: Docker with GPU Support (Recommended)

#### NVIDIA CUDA

```bash
# CUDA 12.0
docker run -p 8080:8080 --gpus all --name local-ai \
  -ti localai/localai:latest-gpu-nvidia-cuda-12

# CUDA 11.7
docker run -p 8080:8080 --gpus all --name local-ai \
  -ti localai/localai:latest-gpu-nvidia-cuda-11
```

**Prerequisites**: Install [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html)

#### AMD ROCm

```bash
docker run -p 8080:8080 \
  --device=/dev/kfd \
  --device=/dev/dri \
  --group-add=video \
  --name local-ai \
  -ti localai/localai:latest-gpu-hipblas
```

#### Intel GPU

```bash
docker run -p 8080:8080 --name local-ai \
  -ti localai/localai:latest-gpu-intel
```

#### Apple Silicon

GPU acceleration works automatically when running on macOS with Apple Silicon. Use the standard CPU image - Metal acceleration is built-in.

### Method 2: AIO Images with GPU

AIO images are also available with GPU support:

```bash
# NVIDIA CUDA 12
docker run -p 8080:8080 --gpus all --name local-ai \
  -ti localai/localai:latest-aio-gpu-nvidia-cuda-12

# AMD
docker run -p 8080:8080 \
  --device=/dev/kfd --device=/dev/dri --group-add=video \
  --name local-ai \
  -ti localai/localai:latest-aio-gpu-hipblas
```

### Method 3: Build from Source

For building with GPU support from source, see the [Build Guide]({{% relref "docs/getting-started/build" %}}).

## Configuring Models for GPU

### Automatic Detection

LocalAI automatically detects GPU capabilities and downloads the appropriate backend when you install models from the gallery.

### Manual Configuration

In your model YAML configuration, specify GPU layers:

```yaml
name: my-model
parameters:
  model: model.gguf
backend: llama-cpp
# Offload layers to GPU (adjust based on your GPU memory)
f16: true
gpu_layers: 35  # Number of layers to offload to GPU
```

**GPU Layers Guidelines**:
- **Small GPU (4-6GB)**: 20-30 layers
- **Medium GPU (8-12GB)**: 30-40 layers
- **Large GPU (16GB+)**: 40+ layers or set to model's total layer count

### Finding the Right Number of Layers

1. Start with a conservative number (e.g., 20)
2. Monitor GPU memory usage: `nvidia-smi` (NVIDIA) or `rocminfo` (AMD)
3. Gradually increase until you reach GPU memory limits
4. For maximum performance, offload all layers if you have enough VRAM

## Verifying GPU Usage

### Check if GPU is Being Used

#### NVIDIA

```bash
# Watch GPU usage in real-time
watch -n 1 nvidia-smi
```

You should see:
- GPU utilization > 0%
- Memory usage increasing
- Processes running on GPU

#### AMD

```bash
rocm-smi
```

#### Check Logs

Enable debug mode to see GPU information in logs:

```bash
DEBUG=true local-ai
```

Look for messages indicating GPU initialization and layer offloading.

## Performance Tips

### 1. Optimize GPU Layers

- Offload as many layers as your GPU memory allows
- Balance between GPU and CPU layers for best performance
- Use `f16: true` for better GPU performance

### 2. Batch Processing

GPU excels at batch processing. Process multiple requests together when possible.

### 3. Model Quantization

Even with GPU, quantized models (Q4_K_M) often provide the best speed/quality balance.

### 4. Context Size

Larger context sizes use more GPU memory. Adjust based on your GPU:

```yaml
context_size: 4096  # Adjust based on GPU memory
```

## Troubleshooting

### GPU Not Detected

1. **Check drivers**: Ensure GPU drivers are installed
2. **Check Docker**: Verify Docker has GPU access
   ```bash
   docker run --rm --gpus all nvidia/cuda:12.0.0-base-ubuntu22.04 nvidia-smi
   ```
3. **Check logs**: Enable debug mode and check for GPU-related errors

### Out of GPU Memory

- Reduce `gpu_layers` in model configuration
- Use a smaller model or lower quantization
- Reduce `context_size`
- Close other GPU-using applications

### Slow Performance

- Ensure you're using the correct GPU image
- Check that layers are actually offloaded (check logs)
- Verify GPU drivers are up to date
- Consider using a more powerful GPU or reducing model size

### CUDA Errors

- Ensure CUDA version matches (11.7 vs 12.0)
- Check CUDA compatibility with your GPU
- Try rebuilding with `REBUILD=true`

## Platform-Specific Notes

### NVIDIA Jetson (L4T)

Use the L4T-specific images:

```bash
docker run -p 8080:8080 --runtime nvidia --gpus all \
  --name local-ai \
  -ti localai/localai:latest-nvidia-l4t-arm64
```

### Apple Silicon

- Metal acceleration is automatic
- No special Docker flags needed
- Use standard CPU images - Metal is built-in
- For best performance, build from source on macOS

## What's Next?

- [GPU Acceleration Documentation]({{% relref "docs/features/gpu-acceleration" %}}) - Detailed GPU information
- [Performance Tuning]({{% relref "docs/advanced/performance-tuning" %}}) - Optimize your setup
- [VRAM Management]({{% relref "docs/advanced/vram-management" %}}) - Manage GPU memory efficiently

## See Also

- [Compatibility Table]({{% relref "docs/reference/compatibility-table" %}}) - GPU support by backend
- [Build Guide]({{% relref "docs/getting-started/build" %}}) - Build with GPU support
- [FAQ]({{% relref "docs/faq" %}}) - Common GPU questions

