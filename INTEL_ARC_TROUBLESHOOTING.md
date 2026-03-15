# Intel Arc GPU Troubleshooting for LocalAI

## Issue: Qwen3.5 and other models fail to load on Intel Arc GPU

Error: `rpc error: code = Unavailable desc = error reading from server: EOF`

## Root Cause

The Intel SYCL backend (`intel-sycl-f16-llama-cpp`) may fail to detect or initialize
the Intel Arc GPU, causing the model loading to fail with a "cannot find preferred 
GPU platform" error.

## Solution

### 1. Verify GPU Detection in Container

Before running LocalAI, verify that the GPU is properly detected:

```bash
docker run -it --rm --entrypoint bash --device /dev/dri localai/localai:latest-gpu-intel
sycl-ls
```

Expected output should show the Intel Arc GPU:
```
[level_zero:gpu][level_zero:0] Intel(R) oneAPI Unified Runtime over Level-Zero, Intel(R) Arc(TM) A770 Graphics
[level_zero:gpu][level_zero:1] Intel(R) oneAPI Unified Runtime over Level-Zero, Intel(R) UHD Graphics
[opencl:cpu][opencl:0] Intel(R) OpenCL
[opencl:gpu][opencl:1] Intel(R) OpenCL Graphics, Intel(R) Arc(TM) A770 Graphics
```

### 2. Proper Device Passthrough

Ensure you're passing the correct device files:

```yaml
devices:
  - /dev/dri/card1    # Arc A770 (card0 is often integrated GPU)
  - /dev/dri/renderD129  # Render device for Arc A770
```

Note: Device numbers vary by system. Use `ls -l /dev/dri/` to identify the correct devices.

### 3. Required Environment Variables

Set the following environment variables:

```yaml
environment:
  - ZES_ENABLE_SYSMAN=1
  - GGML_SYCL_DEVICE=0
  - XPU=1
  - DEBUG=true
```

### 4. Docker Compose Example

```yaml
services:
  local-ai:
    image: localai/localai:latest-gpu-intel
    container_name: local-ai
    environment:
      - MODELS_PATH=/models
      - ZES_ENABLE_SYSMAN=1
      - GGML_SYCL_DEVICE=0
      - XPU=1
      - DEBUG=true
    volumes:
      - ./models:/models
    devices:
      - /dev/dri/card1
      - /dev/dri/renderD129
    group_add:
      - "105"
```

### 5. Verify Device Permissions

Ensure the device files have correct permissions:

```bash
ls -l /dev/dri/
# Expected:
# crw-rw---- 1 root video  226,   0 card0
# crw-rw---- 1 root video  226,   1 card1
# crw-rw-rw- 1 root render 226, 128 renderD128
# crw-rw-rw- 1 root render 226, 129 renderD129
```

If permissions are incorrect, add your user to the video/render groups or adjust permissions.

### 6. Fallback to CPU

If GPU acceleration continues to fail, use the CPU backend:

```yaml
image: localai/localai:latest-cpu
```

## Related Issues

- #3437: Intel ARC GPU - llama_model_load: can not find preferred GPU platform
- #8934: Intel GPU is not utilized by intel-qwen-asr

## References

- [llama.cpp SYCL backend](https://github.com/ggerganov/llama.cpp)
- [Intel oneAPI](https://www.intel.com/content/www/us/en/developer/tools/oneapi/overview.html)
