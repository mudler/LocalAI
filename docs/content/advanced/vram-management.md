+++
disableToc = false
title = "VRAM and Memory Management"
weight = 22
url = '/advanced/vram-management'
+++

When running multiple models in LocalAI, especially on systems with limited GPU memory (VRAM), you may encounter situations where loading a new model fails because there isn't enough available VRAM. LocalAI provides two mechanisms to automatically manage model memory allocation and prevent VRAM exhaustion.

## The Problem

By default, LocalAI keeps models loaded in memory once they're first used. This means:
- If you load a large model that uses most of your VRAM, subsequent requests for other models may fail
- Models remain in memory even when not actively being used
- There's no automatic mechanism to unload models to make room for new ones, unless done manually via the web interface

This is a common issue when working with GPU-accelerated models, as VRAM is typically more limited than system RAM. For more context, see issues [#6068](https://github.com/mudler/LocalAI/issues/6068), [#7269](https://github.com/mudler/LocalAI/issues/7269), and [#5352](https://github.com/mudler/LocalAI/issues/5352).

## Solution 1: Single Active Backend

The simplest approach is to ensure only one model is loaded at a time. When a new model is requested, LocalAI will automatically unload the currently active model before loading the new one.

### Configuration

```bash
./local-ai --single-active-backend

LOCALAI_SINGLE_ACTIVE_BACKEND=true ./local-ai
```

### Use cases

- Single GPU systems with limited VRAM
- When you only need one model active at a time
- Simple deployments where model switching is acceptable

### Example

```bash
LOCALAI_SINGLE_ACTIVE_BACKEND=true ./local-ai

curl http://localhost:8080/v1/chat/completions -d '{"model": "model-a", ...}'

curl http://localhost:8080/v1/chat/completions -d '{"model": "model-b", ...}'
```

## Solution 2: Watchdog Mechanisms

For more flexible memory management, LocalAI provides watchdog mechanisms that automatically unload models based on their activity state. This allows multiple models to be loaded simultaneously, but automatically frees memory when models become inactive or stuck.

> **Note:** Watchdog settings can be configured via the [Runtime Settings]({{%relref "features/runtime-settings#watchdog-settings" %}}) web interface, which allows you to adjust settings without restarting the application.

### Idle Watchdog

The idle watchdog monitors models that haven't been used for a specified period and automatically unloads them to free VRAM.

#### Configuration

Via environment variables or CLI:
```bash
LOCALAI_WATCHDOG_IDLE=true ./local-ai

LOCALAI_WATCHDOG_IDLE=true LOCALAI_WATCHDOG_IDLE_TIMEOUT=10m ./local-ai

./local-ai --enable-watchdog-idle --watchdog-idle-timeout=10m
```

Via web UI: Navigate to Settings → Watchdog Settings and enable "Watchdog Idle Enabled" with your desired timeout.

### Busy Watchdog

The busy watchdog monitors models that have been processing requests for an unusually long time and terminates them if they exceed a threshold. This is useful for detecting and recovering from stuck or hung backends.

#### Configuration

Via environment variables or CLI:
```bash
LOCALAI_WATCHDOG_BUSY=true ./local-ai

LOCALAI_WATCHDOG_BUSY=true LOCALAI_WATCHDOG_BUSY_TIMEOUT=10m ./local-ai

./local-ai --enable-watchdog-busy --watchdog-busy-timeout=10m
```

Via web UI: Navigate to Settings → Watchdog Settings and enable "Watchdog Busy Enabled" with your desired timeout.

### Combined Configuration

You can enable both watchdogs simultaneously for comprehensive memory management:

```bash
LOCALAI_WATCHDOG_IDLE=true \
LOCALAI_WATCHDOG_IDLE_TIMEOUT=15m \
LOCALAI_WATCHDOG_BUSY=true \
LOCALAI_WATCHDOG_BUSY_TIMEOUT=5m \
./local-ai
```

Or using command line flags:

```bash
./local-ai \
  --enable-watchdog-idle --watchdog-idle-timeout=15m \
  --enable-watchdog-busy --watchdog-busy-timeout=5m
```

### Use cases

- Multi-model deployments where different models may be used intermittently
- Systems where you want to keep frequently-used models loaded but free memory from unused ones
- Recovery from stuck or hung backend processes
- Production environments requiring automatic resource management

### Example

```bash
LOCALAI_WATCHDOG_IDLE=true \
LOCALAI_WATCHDOG_IDLE_TIMEOUT=10m \
LOCALAI_WATCHDOG_BUSY=true \
LOCALAI_WATCHDOG_BUSY_TIMEOUT=5m \
./local-ai

curl http://localhost:8080/v1/chat/completions -d '{"model": "model-a", ...}'
curl http://localhost:8080/v1/chat/completions -d '{"model": "model-b", ...}'

```

### Timeout Format

Timeouts can be specified using Go's duration format:
- `15m` - 15 minutes
- `1h` - 1 hour
- `30s` - 30 seconds
- `2h30m` - 2 hours and 30 minutes

## Limitations and Considerations

### VRAM Usage Estimation

LocalAI cannot reliably estimate VRAM usage of new models to load across different backends (llama.cpp, vLLM, diffusers, etc.) because:
- Different backends report memory usage differently
- VRAM requirements vary based on model architecture, quantization, and configuration
- Some backends may not expose memory usage information before loading the model

### Manual Management

If automatic management doesn't meet your needs, you can manually stop models using the LocalAI management API:

```bash
curl -X POST http://localhost:8080/backend/shutdown \
  -H "Content-Type: application/json" \
  -d '{"model": "model-name"}'
```

To stop all models, you'll need to call the endpoint for each loaded model individually, or use the web UI to stop all models at once.

### Best Practices

1. **Monitor VRAM usage**: Use `nvidia-smi` (for NVIDIA GPUs) or similar tools to monitor actual VRAM usage
2. **Start with single active backend**: For single-GPU systems, `--single-active-backend` is often the simplest solution
3. **Tune watchdog timeouts**: Adjust timeouts based on your usage patterns - shorter timeouts free memory faster but may cause more frequent reloads
4. **Consider model size**: Ensure your VRAM can accommodate at least one of your largest models
5. **Use quantization**: Smaller quantized models use less VRAM and allow more flexibility

## Related Documentation

- See [Advanced Usage]({{%relref "advanced/advanced-usage" %}}) for other configuration options
- See [GPU Acceleration]({{%relref "features/GPU-acceleration" %}}) for GPU setup and configuration
- See [Backend Flags]({{%relref "advanced/advanced-usage#backend-flags" %}}) for all available backend configuration options

