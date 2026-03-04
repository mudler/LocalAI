+++
disableToc = false
title = "VRAM and Memory Management"
weight = 22
url = '/advanced/vram-management'
+++

When running multiple models in LocalAI, especially on systems with limited GPU memory (VRAM), you may encounter situations where loading a new model fails because there isn't enough available VRAM. LocalAI provides several mechanisms to automatically manage model memory allocation and prevent VRAM exhaustion:

1. **Max Active Backends (LRU Eviction)**: Limit the number of loaded models, evicting the least recently used when the limit is reached
2. **Watchdog Mechanisms**: Automatically unload idle or stuck models based on configurable timeouts

## The Problem

By default, LocalAI keeps models loaded in memory once they're first used. This means:
- If you load a large model that uses most of your VRAM, subsequent requests for other models may fail
- Models remain in memory even when not actively being used
- There's no automatic mechanism to unload models to make room for new ones, unless done manually via the web interface

This is a common issue when working with GPU-accelerated models, as VRAM is typically more limited than system RAM. For more context, see issues [#6068](https://github.com/mudler/LocalAI/issues/6068), [#7269](https://github.com/mudler/LocalAI/issues/7269), and [#5352](https://github.com/mudler/LocalAI/issues/5352).

## Solution 1: Max Active Backends (LRU Eviction)

LocalAI supports limiting the maximum number of active backends (loaded models) using LRU (Least Recently Used) eviction. When the limit is reached and a new model needs to be loaded, the least recently used model is automatically unloaded to make room.

### Configuration

Set the maximum number of active backends using CLI flags or environment variables:

```bash
# Allow up to 3 models loaded simultaneously
./local-ai --max-active-backends=3

# Using environment variables
LOCALAI_MAX_ACTIVE_BACKENDS=3 ./local-ai
MAX_ACTIVE_BACKENDS=3 ./local-ai
```

Setting the limit to `1` is equivalent to single active backend mode (see below). Setting to `0` disables the limit (unlimited backends).

### Use cases

- Systems with limited VRAM that can handle a few models simultaneously
- Multi-model deployments where you want to keep frequently-used models loaded
- Balancing between memory usage and model reload times
- Production environments requiring predictable memory consumption

### How it works

1. When a model is requested, its "last used" timestamp is updated
2. When a new model needs to be loaded and the limit is reached, LocalAI identifies the least recently used model(s)
3. The LRU model(s) are automatically unloaded to make room for the new model
4. Concurrent requests for loading different models are handled safely - the system accounts for models currently being loaded when calculating evictions

### Eviction Behavior with Active Requests

By default, LocalAI will **skip evicting models that have active API calls** to prevent interrupting ongoing requests. This means:

- If all models are busy (have active requests), eviction will be skipped and the system will wait for models to become idle
- The loading request will retry eviction with configurable retry settings
- This ensures data integrity and prevents request failures

You can configure this behavior via WebUI or using the following settings:

#### Force Eviction When Busy

To allow evicting models even when they have active API calls (not recommended for production):

```bash
# Via CLI
./local-ai --force-eviction-when-busy

# Via environment variable
LOCALAI_FORCE_EVICTION_WHEN_BUSY=true ./local-ai
```

> **Warning:** Enabling force eviction can interrupt active requests and cause errors. Only use this if you understand the implications.

#### LRU Eviction Retry Settings

When models are busy and cannot be evicted, LocalAI will retry eviction with configurable settings:

```bash
# Configure maximum retries (default: 30)
./local-ai --lru-eviction-max-retries=50

# Configure retry interval (default: 1s)
./local-ai --lru-eviction-retry-interval=2s

# Using environment variables
LOCALAI_LRU_EVICTION_MAX_RETRIES=50 \
LOCALAI_LRU_EVICTION_RETRY_INTERVAL=2s \
./local-ai
```

These settings control how long the system will wait for busy models to become idle before giving up. The retry mechanism allows busy models to complete their requests before being evicted, preventing request failures.

### Example

```bash
# Allow 2 active backends
LOCALAI_MAX_ACTIVE_BACKENDS=2 ./local-ai

# First request - model-a is loaded (1 active)
curl http://localhost:8080/v1/chat/completions -d '{"model": "model-a", ...}'

# Second request - model-b is loaded (2 active, at limit)
curl http://localhost:8080/v1/chat/completions -d '{"model": "model-b", ...}'

# Third request - model-a is evicted (LRU), model-c is loaded
curl http://localhost:8080/v1/chat/completions -d '{"model": "model-c", ...}'

# Request for model-b updates its "last used" time
curl http://localhost:8080/v1/chat/completions -d '{"model": "model-b", ...}'
```

### Single Active Backend Mode

The simplest approach is to ensure only one model is loaded at a time. This is now implemented as `--max-active-backends=1`. When a new model is requested, LocalAI will automatically unload the currently active model before loading the new one.

```bash
# These are equivalent:
./local-ai --max-active-backends=1
./local-ai --single-active-backend

# Using environment variables
LOCALAI_MAX_ACTIVE_BACKENDS=1 ./local-ai
LOCALAI_SINGLE_ACTIVE_BACKEND=true ./local-ai
```

> **Note:** The `--single-active-backend` flag is deprecated but still supported for backward compatibility. It is recommended to use `--max-active-backends=1` instead.

#### Single backend use cases

- Single GPU systems with very limited VRAM
- When you only need one model active at a time
- Simple deployments where model switching is acceptable

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

## Combining LRU and Watchdog

You can combine Max Active Backends (LRU eviction) with the watchdog mechanisms for comprehensive memory management:

```bash
# Allow up to 3 active backends with idle watchdog
LOCALAI_MAX_ACTIVE_BACKENDS=3 \
LOCALAI_WATCHDOG_IDLE=true \
LOCALAI_WATCHDOG_IDLE_TIMEOUT=15m \
./local-ai
```

Or using command line flags:

```bash
./local-ai \
  --max-active-backends=3 \
  --enable-watchdog-idle --watchdog-idle-timeout=15m
```

This configuration:
- Ensures no more than 3 models are loaded at once (LRU eviction kicks in when exceeded)
- Automatically unloads any model that hasn't been used for 15 minutes
- Provides both hard limits and time-based cleanup

### Example with Retry Settings

You can also configure retry behavior when models are busy:

```bash
# Allow up to 2 active backends with custom retry settings
LOCALAI_MAX_ACTIVE_BACKENDS=2 \
LOCALAI_LRU_EVICTION_MAX_RETRIES=50 \
LOCALAI_LRU_EVICTION_RETRY_INTERVAL=2s \
./local-ai
```

Or using command line flags:

```bash
./local-ai \
  --max-active-backends=2 \
  --lru-eviction-max-retries=50 \
  --lru-eviction-retry-interval=2s
```

This configuration:
- Limits to 2 active backends
- Will retry eviction up to 50 times if models are busy
- Waits 2 seconds between retry attempts
- Ensures busy models have time to complete their requests before eviction

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
2. **Set an appropriate backend limit**: For single-GPU systems, `--max-active-backends=1` is often the simplest solution. For systems with more VRAM, you can increase the limit to keep more models loaded
3. **Combine LRU with watchdog**: Use `--max-active-backends` to limit the number of loaded models, and enable idle watchdog to unload models that haven't been used recently
4. **Tune watchdog timeouts**: Adjust timeouts based on your usage patterns - shorter timeouts free memory faster but may cause more frequent reloads
5. **Consider model size**: Ensure your VRAM can accommodate at least one of your largest models
6. **Use quantization**: Smaller quantized models use less VRAM and allow more flexibility

## Related Documentation

- See [Advanced Usage]({{%relref "advanced/advanced-usage" %}}) for other configuration options
- See [GPU Acceleration]({{%relref "features/GPU-acceleration" %}}) for GPU setup and configuration
- See [Backend Flags]({{%relref "advanced/advanced-usage#backend-flags" %}}) for all available backend configuration options

