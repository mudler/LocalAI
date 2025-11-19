+++
disableToc = false
title = "Model Configuration"
weight = 23
url = '/advanced/model-configuration'
+++

LocalAI uses YAML configuration files to define model parameters, templates, and behavior. This page provides a complete reference for all available configuration options.

## Overview

Model configuration files allow you to:
- Define default parameters (temperature, top_p, etc.)
- Configure prompt templates
- Specify backend settings
- Set up function calling
- Configure GPU and memory options
- And much more

## Configuration File Locations

You can create model configuration files in several ways:

1. **Individual YAML files** in the models directory (e.g., `models/gpt-3.5-turbo.yaml`)
2. **Single config file** with multiple models using `--models-config-file` or `LOCALAI_MODELS_CONFIG_FILE`
3. **Remote URLs** - specify a URL to a YAML configuration file at startup

### Example: Basic Configuration

```yaml
name: gpt-3.5-turbo
parameters:
  model: luna-ai-llama2-uncensored.ggmlv3.q5_K_M.bin
  temperature: 0.3

context_size: 512
threads: 10
backend: llama-stable

template:
  completion: completion
  chat: chat
```

### Example: Multiple Models in One File

When using `--models-config-file`, you can define multiple models as a list:

```yaml
- name: model1
  parameters:
    model: model1.bin
  context_size: 512
  backend: llama-stable

- name: model2
  parameters:
    model: model2.bin
  context_size: 1024
  backend: llama-stable
```

## Core Configuration Fields

### Basic Model Settings

| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `name` | string | Model name, used to identify the model in API calls | `gpt-3.5-turbo` |
| `backend` | string | Backend to use (e.g. `llama-cpp`, `vllm`, `diffusers`, `whisper`) | `llama-cpp` |
| `description` | string | Human-readable description of the model | `A conversational AI model` |
| `usage` | string | Usage instructions or notes | `Best for general conversation` |

### Model File and Downloads

| Field | Type | Description |
|-------|------|-------------|
| `parameters.model` | string | Path to the model file (relative to models directory) or URL |
| `download_files` | array | List of files to download. Each entry has `filename`, `uri`, and optional `sha256` |

**Example:**
```yaml
parameters:
  model: my-model.gguf

download_files:
  - filename: my-model.gguf
    uri: https://example.com/model.gguf
    sha256: abc123...
```

## Parameters Section

The `parameters` section contains all OpenAI-compatible request parameters and model-specific options.

### OpenAI-Compatible Parameters

These settings will be used as defaults for all the API calls to the model.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `temperature` | float | `0.9` | Sampling temperature (0.0-2.0). Higher values make output more random |
| `top_p` | float | `0.95` | Nucleus sampling: consider tokens with top_p probability mass |
| `top_k` | int | `40` | Consider only the top K most likely tokens |
| `max_tokens` | int | `0` | Maximum number of tokens to generate (0 = unlimited) |
| `frequency_penalty` | float | `0.0` | Penalty for token frequency (-2.0 to 2.0) |
| `presence_penalty` | float | `0.0` | Penalty for token presence (-2.0 to 2.0) |
| `repeat_penalty` | float | `1.1` | Penalty for repeating tokens |
| `repeat_last_n` | int | `64` | Number of previous tokens to consider for repeat penalty |
| `seed` | int | `-1` | Random seed (omit for random) |
| `echo` | bool | `false` | Echo back the prompt in the response |
| `n` | int | `1` | Number of completions to generate |
| `logprobs` | bool/int | `false` | Return log probabilities of tokens |
| `top_logprobs` | int | `0` | Number of top logprobs to return per token (0-20) |
| `logit_bias` | map | `{}` | Map of token IDs to bias values (-100 to 100) |
| `typical_p` | float | `1.0` | Typical sampling parameter |
| `tfz` | float | `1.0` | Tail free z parameter |
| `keep` | int | `0` | Number of tokens to keep from the prompt |

### Language and Translation

| Field | Type | Description |
|-------|------|-------------|
| `language` | string | Language code for transcription/translation |
| `translate` | bool | Whether to translate audio transcription |

### Custom Parameters

| Field | Type | Description |
|-------|------|-------------|
| `batch` | int | Batch size for processing |
| `ignore_eos` | bool | Ignore end-of-sequence tokens |
| `negative_prompt` | string | Negative prompt for image generation |
| `rope_freq_base` | float32 | RoPE frequency base |
| `rope_freq_scale` | float32 | RoPE frequency scale |
| `negative_prompt_scale` | float32 | Scale for negative prompt |
| `tokenizer` | string | Tokenizer to use (RWKV) |

## LLM Configuration

These settings apply to most LLM backends (llama.cpp, vLLM, etc.):

### Performance Settings

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `threads` | int | `processor count` | Number of threads for parallel computation |
| `context_size` | int | `512` | Maximum context size (number of tokens) |
| `f16` | bool | `false` | Enable 16-bit floating point precision (GPU acceleration) |
| `gpu_layers` | int | `0` | Number of layers to offload to GPU (0 = CPU only) |

### Memory Management

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mmap` | bool | `true` | Use memory mapping for model loading (faster, less RAM) |
| `mmlock` | bool | `false` | Lock model in memory (prevents swapping) |
| `low_vram` | bool | `false` | Use minimal VRAM mode |
| `no_kv_offloading` | bool | `false` | Disable KV cache offloading |

### GPU Configuration

| Field | Type | Description |
|-------|------|-------------|
| `tensor_split` | string | Comma-separated GPU memory allocation (e.g., `"0.8,0.2"` for 80%/20%) |
| `main_gpu` | string | Main GPU identifier for multi-GPU setups |
| `cuda` | bool | Explicitly enable/disable CUDA |

### Sampling and Generation

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mirostat` | int | `0` | Mirostat sampling mode (0=disabled, 1=Mirostat, 2=Mirostat 2.0) |
| `mirostat_tau` | float | `5.0` | Mirostat target entropy |
| `mirostat_eta` | float | `0.1` | Mirostat learning rate |

### LoRA Configuration

| Field | Type | Description |
|-------|------|-------------|
| `lora_adapter` | string | Path to LoRA adapter file |
| `lora_base` | string | Base model for LoRA |
| `lora_scale` | float32 | LoRA scale factor |
| `lora_adapters` | array | Multiple LoRA adapters |
| `lora_scales` | array | Scales for multiple LoRA adapters |

### Advanced Options

| Field | Type | Description |
|-------|------|-------------|
| `no_mulmatq` | bool | Disable matrix multiplication queuing |
| `draft_model` | string | Draft model for speculative decoding |
| `n_draft` | int32 | Number of draft tokens |
| `quantization` | string | Quantization format |
| `load_format` | string | Model load format |
| `numa` | bool | Enable NUMA (Non-Uniform Memory Access) |
| `rms_norm_eps` | float32 | RMS normalization epsilon |
| `ngqa` | int32 | Natural question generation parameter |
| `rope_scaling` | string | RoPE scaling configuration |
| `type` | string | Model type/architecture |
| `grammar` | string | Grammar file path for constrained generation |

### YARN Configuration

YARN (Yet Another RoPE extensioN) settings for context extension:

| Field | Type | Description |
|-------|------|-------------|
| `yarn_ext_factor` | float32 | YARN extension factor |
| `yarn_attn_factor` | float32 | YARN attention factor |
| `yarn_beta_fast` | float32 | YARN beta fast parameter |
| `yarn_beta_slow` | float32 | YARN beta slow parameter |

### Prompt Caching

| Field | Type | Description |
|-------|------|-------------|
| `prompt_cache_path` | string | Path to store prompt cache (relative to models directory) |
| `prompt_cache_all` | bool | Cache all prompts automatically |
| `prompt_cache_ro` | bool | Read-only prompt cache |

### Text Processing

| Field | Type | Description |
|-------|------|-------------|
| `stopwords` | array | Words or phrases that stop generation |
| `cutstrings` | array | Strings to cut from responses |
| `trimspace` | array | Strings to trim whitespace from |
| `trimsuffix` | array | Suffixes to trim from responses |
| `extract_regex` | array | Regular expressions to extract content |

### System Prompt

| Field | Type | Description |
|-------|------|-------------|
| `system_prompt` | string | Default system prompt for the model |

## vLLM-Specific Configuration

These options apply when using the `vllm` backend:

| Field | Type | Description |
|-------|------|-------------|
| `gpu_memory_utilization` | float32 | GPU memory utilization (0.0-1.0, default 0.9) |
| `trust_remote_code` | bool | Trust and execute remote code |
| `enforce_eager` | bool | Force eager execution mode |
| `swap_space` | int | Swap space in GB |
| `max_model_len` | int | Maximum model length |
| `tensor_parallel_size` | int | Tensor parallelism size |
| `disable_log_stats` | bool | Disable logging statistics |
| `dtype` | string | Data type (e.g., `float16`, `bfloat16`) |
| `flash_attention` | string | Flash attention configuration |
| `cache_type_k` | string | Key cache type |
| `cache_type_v` | string | Value cache type |
| `limit_mm_per_prompt` | object | Limit multimodal content per prompt: `{image: int, video: int, audio: int}` |

## Template Configuration

Templates use Go templates with [Sprig functions](http://masterminds.github.io/sprig/).

| Field | Type | Description |
|-------|------|-------------|
| `template.chat` | string | Template for chat completion endpoint |
| `template.chat_message` | string | Template for individual chat messages |
| `template.completion` | string | Template for text completion |
| `template.edit` | string | Template for edit operations |
| `template.function` | string | Template for function/tool calls |
| `template.multimodal` | string | Template for multimodal interactions |
| `template.reply_prefix` | string | Prefix to add to model replies |
| `template.use_tokenizer_template` | bool | Use tokenizer's built-in template (vLLM/transformers) |
| `template.join_chat_messages_by_character` | string | Character to join chat messages (default: `\n`) |

### Template Variables

Templating supports [sprig](https://masterminds.github.io/sprig/) functions.

Following are common variables available in templates:
- `{{.Input}}` - User input
- `{{.Instruction}}` - Instruction for edit operations
- `{{.System}}` - System message
- `{{.Prompt}}` - Full prompt
- `{{.Functions}}` - Function definitions (for function calling)
- `{{.FunctionCall}}` - Function call result

### Example Template

```yaml
template:
  chat: |
    {{.System}}
    {{range .Messages}}
    {{if eq .Role "user"}}User: {{.Content}}{{end}}
    {{if eq .Role "assistant"}}Assistant: {{.Content}}{{end}}
    {{end}}
    Assistant:
```

## Function Calling Configuration

Configure how the model handles function/tool calls:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `function.disable_no_action` | bool | `false` | Disable the no-action behavior |
| `function.no_action_function_name` | string | `answer` | Name of the no-action function |
| `function.no_action_description_name` | string | | Description for no-action function |
| `function.function_name_key` | string | `name` | JSON key for function name |
| `function.function_arguments_key` | string | `arguments` | JSON key for function arguments |
| `function.response_regex` | array | | Named regex patterns to extract function calls |
| `function.argument_regex` | array | | Named regex to extract function arguments |
| `function.argument_regex_key_name` | string | `key` | Named regex capture for argument key |
| `function.argument_regex_value_name` | string | `value` | Named regex capture for argument value |
| `function.json_regex_match` | array | | Regex patterns to match JSON in tool mode |
| `function.replace_function_results` | array | | Replace function call results with patterns |
| `function.replace_llm_results` | array | | Replace LLM results with patterns |
| `function.capture_llm_results` | array | | Capture LLM results as text (e.g., for "thinking" blocks) |

### Grammar Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `function.grammar.disable` | bool | `false` | Completely disable grammar enforcement |
| `function.grammar.parallel_calls` | bool | `false` | Allow parallel function calls |
| `function.grammar.mixed_mode` | bool | `false` | Allow mixed-mode grammar enforcing |
| `function.grammar.no_mixed_free_string` | bool | `false` | Disallow free strings in mixed mode |
| `function.grammar.disable_parallel_new_lines` | bool | `false` | Disable parallel processing for new lines |
| `function.grammar.prefix` | string | | Prefix to add before grammar rules |
| `function.grammar.expect_strings_after_json` | bool | `false` | Expect strings after JSON data |

## Diffusers Configuration

For image generation models using the `diffusers` backend:

| Field | Type | Description |
|-------|------|-------------|
| `diffusers.cuda` | bool | Enable CUDA for diffusers |
| `diffusers.pipeline_type` | string | Pipeline type (e.g., `stable-diffusion`, `stable-diffusion-xl`) |
| `diffusers.scheduler_type` | string | Scheduler type (e.g., `euler`, `ddpm`) |
| `diffusers.enable_parameters` | string | Comma-separated parameters to enable |
| `diffusers.cfg_scale` | float32 | Classifier-free guidance scale |
| `diffusers.img2img` | bool | Enable image-to-image transformation |
| `diffusers.clip_skip` | int | Number of CLIP layers to skip |
| `diffusers.clip_model` | string | CLIP model to use |
| `diffusers.clip_subfolder` | string | CLIP model subfolder |
| `diffusers.control_net` | string | ControlNet model to use |
| `step` | int | Number of diffusion steps |

## TTS Configuration

For text-to-speech models:

| Field | Type | Description |
|-------|------|-------------|
| `tts.voice` | string | Voice file path or voice ID |
| `tts.audio_path` | string | Path to audio files (for Vall-E) |

## Roles Configuration

Map conversation roles to specific strings:

```yaml
roles:
  user: "### Instruction:"
  assistant: "### Response:"
  system: "### System Instruction:"
```

## Feature Flags

Enable or disable experimental features:

```yaml
feature_flags:
  feature_name: true
  another_feature: false
```

## MCP Configuration

Model Context Protocol (MCP) configuration:

| Field | Type | Description |
|-------|------|-------------|
| `mcp.remote` | string | YAML string defining remote MCP servers |
| `mcp.stdio` | string | YAML string defining STDIO MCP servers |

## Agent Configuration

Agent/autonomous agent configuration:

| Field | Type | Description |
|-------|------|-------------|
| `agent.max_attempts` | int | Maximum number of attempts |
| `agent.max_iterations` | int | Maximum number of iterations |
| `agent.enable_reasoning` | bool | Enable reasoning capabilities |
| `agent.enable_planning` | bool | Enable planning capabilities |
| `agent.enable_mcp_prompts` | bool | Enable MCP prompts |
| `agent.enable_plan_re_evaluator` | bool | Enable plan re-evaluation |

## Pipeline Configuration

Define pipelines for audio-to-audio processing:

| Field | Type | Description |
|-------|------|-------------|
| `pipeline.tts` | string | TTS model name |
| `pipeline.llm` | string | LLM model name |
| `pipeline.transcription` | string | Transcription model name |
| `pipeline.vad` | string | Voice activity detection model name |

## gRPC Configuration

Backend gRPC communication settings:

| Field | Type | Description |
|-------|------|-------------|
| `grpc.attempts` | int | Number of retry attempts |
| `grpc.attempts_sleep_time` | int | Sleep time between retries (seconds) |

## Overrides

Override model configuration values at runtime (llama.cpp):

```yaml
overrides:
  - "qwen3moe.expert_used_count=int:10"
  - "some_key=string:value"
```

Format: `KEY=TYPE:VALUE` where TYPE is `int`, `float`, `string`, or `bool`.

## Known Use Cases

Specify which endpoints this model supports:

```yaml
known_usecases:
  - chat
  - completion
  - embeddings
```

Available flags: `chat`, `completion`, `edit`, `embeddings`, `rerank`, `image`, `transcript`, `tts`, `sound_generation`, `tokenize`, `vad`, `video`, `detection`, `llm` (combination of CHAT, COMPLETION, EDIT).

## Complete Example

Here's a comprehensive example combining many options:

```yaml
name: my-llm-model
description: A high-performance LLM model
backend: llama-stable

parameters:
  model: my-model.gguf
  temperature: 0.7
  top_p: 0.9
  top_k: 40
  max_tokens: 2048

context_size: 4096
threads: 8
f16: true
gpu_layers: 35

system_prompt: "You are a helpful AI assistant."

template:
  chat: |
    {{.System}}
    {{range .Messages}}
    {{if eq .Role "user"}}User: {{.Content}}
    {{else if eq .Role "assistant"}}Assistant: {{.Content}}
    {{end}}
    {{end}}
    Assistant:

roles:
  user: "User:"
  assistant: "Assistant:"
  system: "System:"

stopwords:
  - "\n\nUser:"
  - "\n\nHuman:"

prompt_cache_path: "cache/my-model"
prompt_cache_all: true

function:
  grammar:
    parallel_calls: true
    mixed_mode: false

feature_flags:
  experimental_feature: true
```

## Related Documentation

- See [Advanced Usage]({{%relref "advanced/advanced-usage" %}}) for other configuration options
- See [Prompt Templates]({{%relref "advanced/advanced-usage#prompt-templates" %}}) for template examples
- See [CLI Reference]({{%relref "reference/cli-reference" %}}) for command-line options

