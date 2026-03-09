# LocalAI CLI Flags Reference

## Overview

This document provides comprehensive documentation for all CLI flags available in LocalAI, including their syntax, dependencies, mutual exclusions, and common use cases.

## Flag Categories

### Storage Flags

| Flag | Type | Default | Description | Notes |
|------|------|---------|-------------|-------|
| `--backends-path` | string | `${basepath}/backends` | Path containing backends used for inferencing | |
| `--backends-system-path` | string | `/var/lib/local-ai/backends` | Path containing system backends | |
| `--models-path` | string | `${basepath}/models` | Path containing models | |
| `--generated-content-path` | string | `/tmp/generated/content` | Location for generated content | |
| `--upload-path` | string | `/tmp/localai/upload` | Path for file uploads | |
| `--data-path` | string | `${basepath}/data` | Persistent data storage | |
| `--localai-config-dir` | string | `${basepath}/configuration` | Dynamic config directory | |
| `--localai-config-dir-poll-interval` | duration | - | Config dir polling interval (e.g., 1m) | |
| `--models-config-file` | string | - | YAML config for models | Alias: `--config-file` |

### Model Flags

| Flag | Type | Default | Description | Notes |
|------|------|---------|-------------|-------|
| `--galleries` | string | `${galleries}` | JSON list of model galleries | |
| `--backend-galleries` | string | `${backends}` | JSON list of backend galleries | |
| `--autoload-galleries` | bool | true | Auto-load model galleries | |
| `--autoload-backend-galleries` | bool | true | Auto-load backend galleries | |
| `--preload-models` | string | - | JSON string of models to preload | |
| `--preload-models-config` | string | - | Path to YAML config for preloading | |
| `--models` | string[] | - | List of model URLs to load | Can use positional args |
| `--load-to-memory` | string[] | - | Models to load into memory at startup | |

### Performance Flags

| Flag | Type | Default | Description | Notes |
|------|------|---------|-------------|-------|
| `--f16` | bool | false | Enable GPU acceleration | |
| `--threads` | int | - | Number of threads for computation | Short: `-t` |
| `--context-size` | int | - | Default context size for models | |

### API Flags

| Flag | Type | Default | Description | Notes |
|------|------|---------|-------------|-------|
| `--address` | string | `:8080` | Bind address for API server | |
| `--cors` | bool | false | Enable CORS | |
| `--cors-allow-origins` | string | - | CORS allowed origins | Requires `--cors` |
| `--csrf` | bool | false | Enable CSRF middleware | |
| `--upload-limit` | int | 15 | Upload limit in MB | |
| `--api-key` | string[] | - | API keys for authentication | |
| `--disable-webui` | bool | false | Disable web UI | |
| `--disable-runtime-settings` | bool | false | Disable runtime settings | |
| `--disable-metrics-endpoint` | bool | false | Disable /metrics endpoint | |
| `--disable-gallery-endpoint` | bool | false | Disable gallery endpoints | |
| `--disable-mcp` | bool | false | Disable MCP support | |
| `--machine-tag` | string | - | Add Machine-Tag header | |
| `--enable-tracing` | bool | false | Enable API tracing | |
| `--tracing-max-items` | int | 1024 | Maximum traces to keep | |
| `--agent-job-retention-days` | int | 30 | Agent job history retention | |
| `--open-responses-store-ttl` | string | `0` | TTL for responses store | |

### Backend Flags

| Flag | Type | Default | Description | Notes |
|------|------|---------|-------------|-------|
| `--external-backends` | string[] | - | External backends to load | |
| `--external-grpc-backends` | string[] | - | External gRPC backends | |
| `--parallel-requests` | bool | false | Allow parallel backend requests | |
| `--single-active-backend` | bool | false | Single backend mode | Deprecated: use `--max-active-backends=1` |
| `--max-active-backends` | int | 0 | Max backends loaded (0=unlimited) | |
| `--preload-backend-only` | bool | false | Only preload, don't start API | |
| `--enable-watchdog-idle` | bool | false | Watchdog for idle backends | |
| `--watchdog-idle-timeout` | string | `15m` | Idle timeout threshold | |
| `--enable-watchdog-busy` | bool | false | Watchdog for busy backends | |
| `--watchdog-busy-timeout` | string | `5m` | Busy timeout threshold | |
| `--watchdog-interval` | string | `500ms` | Watchdog check interval | |
| `--enable-memory-reclaimer` | bool | false | Auto-evict on memory threshold | |
| `--memory-reclaimer-threshold` | float | 0.95 | Memory threshold (0.0-1.0) | |
| `--force-eviction-when-busy` | bool | false | Evict even with active calls | |
| `--lru-eviction-max-retries` | int | 30 | Max retries for eviction | |
| `--lru-eviction-retry-interval` | string | `1s` | Retry interval | |


### P2P Flags

| Flag | Type | Default | Description | Notes |
|------|------|---------|-------------|-------|
| `--p2p` | bool | false | Enable P2P mode | |
| `--p2p-dht-interval` | int | 360 | DHT refresh interval | |
| `--p2p-otp-interval` | int | 9000 | OTP refresh interval | |
| `--p2p-token` | string | - | Token for P2P mode | Alias: `--p2ptoken` (deprecated) |
| `--p2p-network-id` | string | - | Network ID for P2P grouping | |

### Hardening Flags

| Flag | Type | Default | Description | Notes |
|------|------|---------|-------------|-------|
| `--disable-predownload-scan` | bool | false | Disable security scanner | |
| `--opaque-errors` | bool | false | Return blank 500 errors | Security hardening |
| `--use-subtle-key-comparison` | bool | false | Constant-time key comparison | Anti-timing attacks |
| `--disable-api-key-requirement-for-http-get` | bool | false | Skip API key for GET | Testing only |
| `--http-get-exempted-endpoints` | string[] | see below | Endpoints exempted from GET check | |

Default exempted endpoints:
```
^/$, ^/browse/?$, ^/talk/?$, ^/p2p/?$, ^/chat/?$, ^/image/?$, ^/text2image/?$, ^/tts/?$, ^/static/.*$, ^/swagger.*$
```

### Federated Flags

| Flag | Type | Default | Description | Notes |
|------|------|---------|-------------|-------|
| `--federated` | bool | false | Enable federated instance | |

### Agent Pool Flags

| Flag | Type | Default | Description | Notes |
|------|------|---------|-------------|-------|
| `--disable-agents` | bool | false | Disable agent pool | |
| `--agent-pool-api-url` | string | - | API URL for agents | |
| `--agent-pool-api-key` | string | - | API key for agents | |
| `--agent-pool-default-model` | string | - | Default model for agents | |
| `--agent-pool-multimodal-model` | string | - | Multimodal model | |
| `--agent-pool-transcription-model` | string | - | Transcription model | |
| `--agent-pool-transcription-language` | string | - | Transcription language | |
| `--agent-pool-tts-model` | string | - | TTS model | |
| `--agent-pool-state-dir` | string | - | State directory | |
| `--agent-pool-timeout` | string | 5m | Agent timeout | |
| `--agent-pool-enable-skills` | bool | false | Enable skills service | |
| `--agent-pool-vector-engine` | string | chromem | Vector engine type | |
| `--agent-pool-embedding-model` | string | granite-... | Embedding model | |
| `--agent-pool-custom-actions-dir` | string | - | Custom actions directory | |
| `--agent-pool-database-url` | string | - | Database URL | |
| `--agent-pool-max-chunking-size` | int | 400 | Max chunking size | |
| `--agent-pool-chunk-overlap` | int | 0 | Chunk overlap | |
| `--agent-pool-enable-logs` | bool | false | Enable agent logging | |
| `--agent-pool-collection-db-path` | string | - | Collections DB path | |
| `--agent-hub-url` | string | https://agenthub.localai.io | Agent hub URL | |


## Flag Dependencies and Constraints

### Required Flag Combinations

Some flags require other flags to be set for meaningful operation:

| Flag | Requires | Reason |
|------|----------|--------|
| `--cors-allow-origins` | `--cors` | CORS must be enabled first |
| `--watchdog-idle-timeout` | `--enable-watchdog-idle` | Watchdog must be enabled |
| `--watchdog-busy-timeout` | `--enable-watchdog-busy` | Watchdog must be enabled |
| `--watchdog-interval` | Either watchdog | Watchdog must be enabled |
| `--memory-reclaimer-threshold` | `--enable-memory-reclaimer` | Memory reclaimer must be enabled |

### Mutually Exclusive Flags

These flags cannot be used together:

| Flag A | Flag B | Reason |
|--------|--------|--------|
| `--single-active-backend` | `--max-active-backends` | Both control backend limits |
| `--preload-models` | `--preload-models-config` | Both specify preload sources |
| `--disable-api-key-requirement-for-http-get` | `--api-key` (security) | Contradictory security settings |

### Deprecated Flags

| Deprecated Flag | Replacement | Status |
|-----------------|-------------|--------|
| `--p2ptoken` | `--p2p-token` | Still supported via alias |
| `--config-file` | `--models-config-file` | Still supported via alias |
| `--single-active-backend` | `--max-active-backends=1` | Still supported |


## Common Use Cases

### Basic Server Startup

```bash
# Simple startup with default settings
localai start

# Start with a specific model
localai start --models https://github.com/lmstudio-com/llama-cpp-python/releases/download/v0.1/litellm.gguf

# Start with multiple models
localai start model1.gguf model2.gguf
```

### Production Configuration

```bash
localai start \\
  --models-path /opt/localai/models \\
  --backends-path /opt/localai/backends \\
  --address :8080 \\
  --api-key my-secret-key \\
  --f16 \\
  --threads 8 \\
  --context-size 4096 \\
  --enable-memory-reclaimer \\
  --memory-reclaimer-threshold 0.9
```

### Development with CORS

```bash
localai start \\
  --address :8080 \\
  --cors \\
  --cors-allow-origins "http://localhost:3000,http://localhost:5173" \\
  --models-path ./models
```

### P2P Mode

```bash
localai start \\
  --p2p \\
  --p2p-token my-p2p-token \\
  --p2p-network-id my-network
```

### High-Availability Setup

```bash
localai start \\
  --max-active-backends 4 \\
  --enable-watchdog-idle \\
  --watchdog-idle-timeout 30m \\
  --enable-watchdog-busy \\
  --watchdog-busy-timeout 10m \\
  --parallel-requests
```

### Agent Pool Configuration

```bash
localai start \\
  --agent-pool-api-url http://localhost:8080 \\
  --agent-pool-default-model mistral-7b-instruct \\
  --agent-pool-enable-skills \\
  --agent-pool-vector-engine chromem
```

### Security Hardening

```bash
localai start \\
  --api-key my-secret-key \\
  --opaque-errors \\
  --use-subtle-key-comparison \\
  --disable-predownload-scan \\
  --enable-watchdog-idle
```


## Flag Compatibility Matrix

### Backend Management Flags

| | `--max-active-backends` | `--parallel-requests` | `--preload-backend-only` | `--enable-memory-reclaimer` |
|---|:---:|:---:|:---:|:---:|
| `--max-active-backends` | - | ✅ | ✅ | ✅ |
| `--parallel-requests` | ✅ | - | ⚠️ | ✅ |
| `--preload-backend-only` | ✅ | ⚠️ | - | ⚠️ |
| `--enable-memory-reclaimer` | ✅ | ✅ | ⚠️ | - |

Legend: ✅ = Fully Compatible | ⚠️ = Limited/Conditional | ❌ = Incompatible

### Watchdog Flags

| | `--enable-watchdog-idle` | `--enable-watchdog-busy` | `--watchdog-interval` |
|---|:---:|:---:|:---:|
| `--enable-watchdog-idle` | - | ✅ | ✅ |
| `--enable-watchdog-busy` | ✅ | - | ✅ |
| `--watchdog-interval` | ✅ | ✅ | - |

### P2P Flags

| | `--p2p` | `--p2p-token` | `--p2p-network-id` | `--federated` |
|---|:---:|:---:|:---:|:---:|
| `--p2p` | - | ✅ | ✅ | ⚠️ |
| `--p2p-token` | ✅ | - | ✅ | ⚠️ |
| `--p2p-network-id` | ✅ | ✅ | - | ⚠️ |
| `--federated` | ⚠️ | ⚠️ | ⚠️ | - |

### Hardening Flags

| | `--opaque-errors` | `--use-subtle-key-comparison` | `--api-key` | `--disable-api-key-requirement-for-http-get` |
|---|:---:|:---:|:---:|:---:|
| `--opaque-errors` | - | ✅ | ✅ | ✅ |
| `--use-subtle-key-comparison` | ✅ | - | ✅ | ❌ |
| `--api-key` | ✅ | ✅ | - | ❌ |
| `--disable-api-key-requirement-for-http-get` | ✅ | ❌ | ❌ | - |

