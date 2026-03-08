+++
disableToc = false
title = "Troubleshooting"
weight = 9
url = '/basics/troubleshooting/'
icon = "build"
+++

This guide covers common issues you may encounter when using LocalAI, organized by category. For each issue, diagnostic steps and solutions are provided.

## Quick Diagnostics

Before diving into specific issues, run these commands to gather diagnostic information:

```bash
# Check LocalAI is running and responsive
curl http://localhost:8080/readyz

# List loaded models
curl http://localhost:8080/v1/models

# Check LocalAI version
local-ai --version

# Enable debug logging for detailed output
DEBUG=true local-ai run
# or
local-ai run --log-level=debug
```

For Docker deployments:

```bash
# View container logs
docker logs local-ai

# Check container status
docker ps -a | grep local-ai

# Test GPU access (NVIDIA)
docker run --rm --gpus all nvidia/cuda:12.8.0-base-ubuntu24.04 nvidia-smi
```

## Installation Issues

### Binary Won't Execute on Linux

**Symptoms:** Permission denied or "cannot execute binary file" errors.

**Solution:**

```bash
chmod +x local-ai-*
./local-ai-Linux-x86_64 run
```

If you see "cannot execute binary file: Exec format error", you downloaded the wrong architecture. Verify with:

```bash
uname -m
# x86_64 → download the x86_64 binary
# aarch64 → download the arm64 binary
```

### macOS: Application Is Quarantined

**Symptoms:** macOS blocks LocalAI from running because the DMG is not signed by Apple.

**Solution:** See [GitHub issue #6268](https://github.com/mudler/LocalAI/issues/6268) for quarantine bypass instructions. This is tracked for resolution in [issue #6244](https://github.com/mudler/LocalAI/issues/6244).




**Solution:** Rebuild with unsupported instructions disabled:

```bash
REBUILD=true CMAKE_ARGS="-DGGML_F16C=OFF -DGGML_AVX512=OFF -DGGML_AVX2=OFF -DGGML_FMA=OFF" local-ai run
```

## Model Loading Problems

### Model Not Found

**Symptoms:** API returns `404` or `"model not found"` error.

**Diagnostic steps:**

1. Check the model exists in your models directory:
   ```bash
   ls -la /path/to/models/
   ```

2. Verify your models path is correct:
   ```bash
   # Check what path LocalAI is using
   local-ai run --models-path /path/to/models --log-level=debug
   ```

3. Confirm the model name matches your request:
   ```bash
   # List available models
   curl http://localhost:8080/v1/models | jq '.data[].id'
   ```

### Model Fails to Load (Backend Error)

**Symptoms:** Model is found but fails to load, with backend errors in the logs.

**Common causes and fixes:**

- **Wrong backend:** Ensure the backend in your model YAML matches the model format. GGUF models use `llama-cpp`, diffusion models use `diffusers`, etc. See the [compatibility table](/docs/reference/compatibility-table/) for details.
- **Backend not installed:** Check installed backends:
  ```bash
  local-ai backends list
  # Install a missing backend:
  local-ai backends install llama-cpp
  ```
- **Corrupt model file:** Re-download the model. Partial downloads or disk errors can corrupt files.
- **Wrong model format:** LocalAI uses GGUF format for llama.cpp models. Older GGML format is deprecated.

### Model Configuration Issues

**Symptoms:** Model loads but produces unexpected results or errors during inference.

Check your model YAML configuration:

```yaml
# Example model config
name: my-model
backend: llama-cpp
parameters:
  model: my-model.gguf  # Relative to models directory
context_size: 2048
threads: 4  # Should match physical CPU cores
```

Common mistakes:
- `model` path must be relative to the models directory, not an absolute path
- `threads` set higher than physical CPU cores causes contention
- `context_size` too large for available RAM causes OOM errors

## GPU and Memory Issues

### GPU Not Detected

**NVIDIA (CUDA):**

```bash
# Verify CUDA is available
nvidia-smi

# For Docker, verify GPU passthrough
docker run --rm --gpus all nvidia/cuda:12.8.0-base-ubuntu24.04 nvidia-smi
```

When working correctly, LocalAI logs should show: `ggml_init_cublas: found X CUDA devices`.

Ensure you are using a CUDA-enabled container image (tags containing `cuda11`, `cuda12`, or `cuda13`). CPU-only images cannot use NVIDIA GPUs.

**AMD (ROCm):**

```bash
# Verify ROCm installation
rocminfo

# Docker requires device passthrough
docker run --device=/dev/kfd --device=/dev/dri --group-add=video ...
```

If your GPU is not in the default target list, set `GPU_TARGETS` to your GPU's LLVM target and rebuild with `REBUILD=true`. Supported targets include: gfx900, gfx906, gfx908, gfx90a, gfx940, gfx941, gfx942, gfx1030, gfx1031, gfx1100, gfx1101.

**Intel (SYCL):**

```bash
# Docker requires device passthrough
docker run --device /dev/dri ...
```

Use container images with `gpu-intel` in the tag. **Known issue:** SYCL hangs when `mmap: true` is set — disable it in your model config:

```yaml
mmap: false
```

**Overriding backend auto-detection:**

If LocalAI picks the wrong GPU backend, override it:

```bash
LOCALAI_FORCE_META_BACKEND_CAPABILITY=nvidia local-ai run
# Options: default, nvidia, amd, intel
```

### Out of Memory (OOM)

**Symptoms:** Model loading fails or the process is killed by the OS.

**Solutions:**

1. **Use smaller quantizations:** Q4_K_S or Q2_K use significantly less memory than Q8_0 or Q6_K
2. **Reduce context size:** Lower `context_size` in your model YAML
3. **Enable low VRAM mode:** Add `low_vram: true` to your model config
4. **Limit active models:** Only keep one model loaded at a time:
   ```bash
   local-ai run --max-active-backends=1
   ```
5. **Enable idle watchdog:** Automatically unload unused models:
   ```bash
   local-ai run --enable-watchdog-idle --watchdog-idle-timeout=10m
   ```
6. **Manually unload a model:**
   ```bash
   curl -X POST http://localhost:8080/backend/shutdown \
     -H "Content-Type: application/json" \
     -d '{"model": "model-name"}'
   ```

### Models Stay Loaded and Consume Memory

By default, models remain loaded in memory after first use. This can exhaust VRAM when switching between models.

**Configure LRU eviction:**

```bash
# Keep at most 2 models loaded; evict least recently used
local-ai run --max-active-backends=2
```

**Configure watchdog auto-unload:**

```bash
local-ai run \
  --enable-watchdog-idle --watchdog-idle-timeout=15m \
  --enable-watchdog-busy --watchdog-busy-timeout=5m
```

These can also be set via environment variables (`LOCALAI_WATCHDOG_IDLE=true`, `LOCALAI_WATCHDOG_IDLE_TIMEOUT=15m`) or in the Web UI under Settings → Watchdog Settings.

See the [VRAM Management guide](/advanced/vram-management/) for more details.

## API Connection Problems

### Connection Refused

**Symptoms:** `curl: (7) Failed to connect to localhost port 8080: Connection refused`

**Diagnostic steps:**

1. Verify LocalAI is running:
   ```bash
   # Direct install
   ps aux | grep local-ai

   # Docker
   docker ps | grep local-ai
   ```

2. Check the bind address and port:
   ```bash
   # Default is :8080. Override with:
   local-ai run --address=0.0.0.0:8080
   # or
   LOCALAI_ADDRESS=":8080" local-ai run
   ```

3. Check for port conflicts:
   ```bash
   ss -tlnp | grep 8080
   ```

### Authentication Errors (401)

**Symptoms:** `401 Unauthorized` response.

If API key authentication is enabled (`LOCALAI_API_KEY` or `--api-keys`), include the key in your requests:

```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer YOUR_API_KEY"
```

Keys can also be passed via `x-api-key` or `xi-api-key` headers.

### Request Errors (400/422)

**Symptoms:** `400 Bad Request` or `422 Unprocessable Entity`.

Common causes:
- Malformed JSON in request body
- Missing required fields (e.g., `model` or `messages`)
- Invalid parameter values (e.g., negative `top_n` for reranking)

Enable debug logging to see the full request/response:

```bash
DEBUG=true local-ai run
```

See the [API Errors reference](/reference/api-errors/) for a complete list of error codes and their meanings.

## Performance Issues

### Slow Inference

**Diagnostic steps:**

1. Enable debug mode to see inference timing:
   ```bash
   DEBUG=true local-ai run
   ```

2. Use streaming to measure time-to-first-token:
   ```bash
   curl http://localhost:8080/v1/chat/completions \
     -H "Content-Type: application/json" \
     -d '{"model": "my-model", "messages": [{"role": "user", "content": "Hello"}], "stream": true}'
   ```

**Common causes and fixes:**

- **Model on HDD:** Move models to an SSD. If stuck with HDD, disable memory mapping (`mmap: false`) to load the model entirely into RAM.
- **Thread overbooking:** Set `--threads` to match your physical CPU core count (not logical/hyperthreaded count).
- **Default sampling:** LocalAI uses mirostat sampling by default, which produces better quality output but is slower. Disable it for benchmarking:
  ```yaml
  # In model config
  mirostat: 0
  ```
- **No GPU offloading:** Ensure `gpu_layers` is set in your model config to offload layers to GPU:
  ```yaml
  gpu_layers: 99  # Offload all layers
  ```
- **Context size too large:** Larger context sizes require more memory and slow down inference. Use the smallest context size that meets your needs.

### High Memory Usage

- Use quantized models (Q4_K_M is a good balance of quality and size)
- Reduce `context_size`
- Enable `low_vram: true` in model config
- Disable `mmlock` (memory locking) if it's enabled
- Set `--max-active-backends=1` to keep only one model in memory

## Docker-Specific Problems

### Container Fails to Start

**Diagnostic steps:**

```bash
# Check container logs
docker logs local-ai

# Check if port is already in use
ss -tlnp | grep 8080

# Verify the image exists
docker images | grep localai
```

### GPU Not Available Inside Container

**NVIDIA:**

```bash
# Ensure nvidia-container-toolkit is installed, then:
docker run --gpus all ...
```

**AMD:**

```bash
docker run --device=/dev/kfd --device=/dev/dri --group-add=video ...
```

**Intel:**

```bash
docker run --device /dev/dri ...
```

### Health Checks Failing

Add a health check to your Docker Compose configuration:

```yaml
services:
  local-ai:
    image: localai/localai:latest
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/readyz"]
      interval: 30s
      timeout: 10s
      retries: 3
```

### Models Not Persisted Between Restarts

Mount a volume for your models directory:

```yaml
services:
  local-ai:
    volumes:
      - ./models:/build/models:cached
```

## Network and P2P Issues

### P2P Workers Not Discovered

**Symptoms:** Distributed inference setup but workers are not found.

**Key requirements:**

- Use `--net host` or `network_mode: host` in Docker
- Share the same P2P token across all nodes

**Debug P2P connectivity:**

```bash
LOCALAI_P2P_LOGLEVEL=debug \
LOCALAI_P2P_LIB_LOGLEVEL=debug \
LOCALAI_P2P_ENABLE_LIMITS=true \
LOCALAI_P2P_TOKEN="<TOKEN>" \
local-ai run
```

**If DHT is causing issues**, try disabling it to use local mDNS discovery instead:

```bash
LOCALAI_P2P_DISABLE_DHT=true local-ai run
```

### P2P Limitations

- Only a single model is currently supported for distributed inference
- Workers must be detected before inference starts — you cannot add workers mid-inference
- Workers mode supports llama-cpp compatible models only

See the [Distributed Inferencing guide](/features/distributed-inferencing/) for full setup instructions.

## Still Having Issues?

If your issue isn't covered here:

1. **Search existing issues:** Check the [GitHub Issues](https://github.com/mudler/LocalAI/issues) for similar problems
2. **Enable debug logging:** Run with `DEBUG=true` or `--log-level=debug` and include the logs when reporting
3. **Open a new issue:** Include your OS, hardware (CPU/GPU), LocalAI version, model being used, full error logs, and steps to reproduce
4. **Community help:** Join the [LocalAI Discord](https://discord.gg/uJAeKSAGDy) for community support
