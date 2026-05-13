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
| `draft_model` | string | Draft model GGUF file for speculative decoding (see [Speculative Decoding](#speculative-decoding)) |
| `n_draft` | int32 | Maximum number of draft tokens per speculative step (default: 16) |
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

### Speculative Decoding

Speculative decoding speeds up text generation by predicting multiple tokens ahead and verifying them in a single forward pass. The output is identical to normal decoding — only faster. This feature is only available with the `llama-cpp` backend.

There are two approaches:

#### Draft Model Speculative Decoding

Uses a smaller, faster model from the same model family to draft candidate tokens, which the main model then verifies. Requires a separate GGUF file for the draft model.

```yaml
name: my-model
backend: llama-cpp
parameters:
  model: large-model.gguf
draft_model: small-draft-model.gguf
n_draft: 8
options:
  - spec_p_min:0.8
  - draft_gpu_layers:99
```

#### N-gram Self-Speculative Decoding

Uses patterns from the token history to predict future tokens — no extra model required. Works well for repetitive or structured output (code, JSON, lists).

```yaml
name: my-model
backend: llama-cpp
parameters:
  model: my-model.gguf
options:
  - spec_type:ngram_simple
  - spec_n_max:16
```

#### Speculative Decoding Options

These are set via the `options:` array in the model configuration (format: `key:value`):

**Common options**

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `spec_type` / `speculative_type` | string | `none` | Speculative decoding type, or comma-separated list to chain multiple (see table below) |
| `spec_n_max` / `draft_max` | int | 16 | Maximum number of tokens to draft per step |
| `spec_n_min` / `draft_min` | int | 0 | Minimum draft tokens required to use speculation |
| `spec_p_min` / `draft_p_min` | float | 0.75 | Minimum probability threshold for greedy acceptance |
| `spec_p_split` | float | 0.1 | Split probability for tree-based branching |

**Draft-model options** (apply when `spec_type=draft`, i.e. a `draft_model` is configured)

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `draft_gpu_layers` | int | -1 | GPU layers for the draft model (-1 = use default) |
| `draft_threads` / `spec_draft_threads` | int | same as main | Threads used by the draft model (`<= 0` = hardware concurrency) |
| `draft_threads_batch` / `spec_draft_threads_batch` | int | same as `draft_threads` | Threads used by the draft model during batch / prompt processing |
| `draft_cache_type_k` / `spec_draft_cache_type_k` | string | `f16` | KV cache K data type for the draft model (same values as `cache_type_k`) |
| `draft_cache_type_v` / `spec_draft_cache_type_v` | string | `f16` | KV cache V data type for the draft model |
| `draft_cpu_moe` / `spec_draft_cpu_moe` | bool | false | Keep all MoE expert weights of the draft model on CPU |
| `draft_n_cpu_moe` / `spec_draft_n_cpu_moe` | int | 0 | Keep MoE expert weights of the first N draft-model layers on CPU |
| `draft_override_tensor` / `spec_draft_override_tensor` | string | "" | Comma-separated `<tensor regex>=<buffer type>` overrides for the draft model |
| `draft_ctx_size` | int | (ignored) | Deprecated upstream: the draft now shares the target context size. Accepted for backward compatibility but has no effect. |

**`ngram_simple` options** (used when `spec_type` includes `ngram_simple`)

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `spec_ngram_size_n` / `ngram_size_n` | int | 12 | N-gram lookup size |
| `spec_ngram_size_m` / `ngram_size_m` | int | 48 | M-gram proposal size |
| `spec_ngram_min_hits` / `ngram_min_hits` | int | 1 | Minimum hits for accepting n-gram proposals |

**`ngram_mod` options** (used when `spec_type` includes `ngram_mod`)

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `spec_ngram_mod_n_min` | int | 48 | Minimum number of ngram tokens to use |
| `spec_ngram_mod_n_max` | int | 64 | Maximum number of ngram tokens to use |
| `spec_ngram_mod_n_match` | int | 24 | Ngram lookup length |

**`ngram_map_k` options** (used when `spec_type` includes `ngram_map_k`)

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `spec_ngram_map_k_size_n` | int | 12 | N-gram lookup size |
| `spec_ngram_map_k_size_m` | int | 48 | M-gram proposal size |
| `spec_ngram_map_k_min_hits` | int | 1 | Minimum hits for accepting proposals |

**`ngram_map_k4v` options** (used when `spec_type` includes `ngram_map_k4v`)

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `spec_ngram_map_k4v_size_n` | int | 12 | N-gram lookup size |
| `spec_ngram_map_k4v_size_m` | int | 48 | M-gram proposal size |
| `spec_ngram_map_k4v_min_hits` | int | 1 | Minimum hits for accepting proposals |

**`ngram_cache` lookup files**

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `spec_lookup_cache_static` / `lookup_cache_static` | string | "" | Path to a static ngram lookup cache file |
| `spec_lookup_cache_dynamic` / `lookup_cache_dynamic` | string | "" | Path to a dynamic ngram lookup cache file (updated by generation) |

#### Speculative Type Values

The canonical names match upstream llama.cpp (dash-separated). For backward compatibility LocalAI also accepts the underscore-separated forms and the bare `draft` / `eagle3` aliases.

| Type | Aliases accepted | Description |
|------|------------------|-------------|
| `none` | | No speculative decoding (default) |
| `draft-simple` | `draft`, `draft_simple` | Draft model-based speculation (auto-set when `draft_model` is configured) |
| `draft-eagle3` | `eagle3`, `draft_eagle3` | EAGLE3 draft model architecture |
| `ngram-simple` | `ngram_simple` | Simple self-speculative using token history |
| `ngram-map-k` | `ngram_map_k` | N-gram with key-only map |
| `ngram-map-k4v` | `ngram_map_k4v` | N-gram with keys and 4 m-gram values |
| `ngram-mod` | `ngram_mod` | Modified n-gram speculation |
| `ngram-cache` | `ngram_cache` | 3-level n-gram cache |

Multiple types can be chained by passing a comma-separated list to `spec_type` (e.g. `spec_type:ngram-simple,ngram-mod`). The runtime tries them in order and accepts the first proposal that meets the acceptance criteria.

{{% notice note %}}
Speculative decoding is automatically disabled when multimodal models (with `mmproj`) are active. The `n_draft` parameter can also be overridden per-request.
{{% /notice %}}

### Reasoning Models (DeepSeek-R1, Qwen3, etc.)

These load-time options control how the backend parses `<think>` reasoning blocks and how much budget the model is allowed for thinking. They are set per model via the `options:` array.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `reasoning_format` | string | `deepseek` | Parser for reasoning/thinking blocks. One of `none`, `auto`, `deepseek`, `deepseek-legacy` (alias `deepseek_legacy`). |
| `enable_reasoning` / `reasoning_budget` | int | `-1` | Reasoning budget in tokens: `-1` unlimited, `0` disabled, `>0` token cap for the thinking section. |
| `prefill_assistant` | bool | `true` | When `false`, the trailing assistant message is not pre-filled by the chat template. |

{{% notice note %}}
This is the load-time reasoning configuration. The orthogonal per-request `enable_thinking` chat-template kwarg (set via the YAML `reasoning.disable` field) toggles thinking on/off per call without restarting the model.
{{% /notice %}}

### Multimodal Backend Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `mmproj_use_gpu` / `mmproj_offload` | bool | `true` | Set `false` to keep the multimodal projector on CPU (saves VRAM at cost of speed). |
| `image_min_tokens` | int | `-1` | Minimum vision tokens per image. `-1` keeps the model default. |
| `image_max_tokens` | int | `-1` | Maximum vision tokens per image. `-1` keeps the model default. |

### Embedding & Reranking Backend Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `pooling_type` / `pooling` | string | auto | Pooling strategy for embeddings: `none`, `mean`, `cls`, `last`, `rank`. Reranking automatically uses `rank`. |
| `embd_normalize` / `embedding_normalize` | int | `2` | Normalization: `-1` none, `0` max-abs, `1` taxicab, `2` Euclidean (L2), `>2` p-norm. |

### Other Backend Tuning Options

These llama.cpp options are passed through the `options:` array.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `n_ubatch` / `ubatch` | int | same as `batch` | Physical batch size. Decouple from `n_batch` when an embedding/rerank workload needs a different value. |
| `threads_batch` / `n_threads_batch` | int | same as `threads` | Threads used during prompt processing. `<= 0` means `hardware_concurrency()`. |
| `direct_io` / `use_direct_io` | bool | `false` | Open the model with `O_DIRECT` (faster cold loads on NVMe; ignored if not supported). |
| `verbosity` | int | `3` | llama.cpp internal log verbosity threshold. Higher = more verbose. |
| `override_tensor` / `tensor_buft_overrides` | string | "" | Per-tensor buffer-type overrides for the main model. Format: `<tensor regex>=<buffer type>,<tensor regex>=<buffer type>,...`. Mirrors the existing `draft_override_tensor` syntax for the draft model. |

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
| `cache_type_k` | string | Key cache quantization type. Maps to llama.cpp's `-ctk`. Accepted values for llama.cpp-family backends (`llama-cpp`, `ik-llama-cpp`, `turboquant`): `f16`, `f32`, `q8_0`, `q4_0`, `q4_1`, `q5_0`, `q5_1`. The `turboquant` backend additionally accepts `turbo2`, `turbo3`, `turbo4` — the fork's TurboQuant KV-cache schemes. `turbo3`/`turbo4` auto-enable flash_attention. |
| `cache_type_v` | string | Value cache quantization type. Maps to llama.cpp's `-ctv`. Same accepted values as `cache_type_k`. Note: any quantized V cache requires flash_attention to be enabled. |
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

## Reasoning Configuration

Configure how reasoning tags are extracted and processed from model output. Reasoning tags are used by models like DeepSeek, Command-R, and others to include internal reasoning steps in their responses.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `reasoning.disable` | bool | `false` | When `true`, disables reasoning extraction entirely. The original content is returned without any processing. |
| `reasoning.disable_reasoning_tag_prefill` | bool | `false` | When `true`, disables automatic prepending of thinking start tokens. Use this when your model already includes reasoning tags in its output format. |
| `reasoning.strip_reasoning_only` | bool | `false` | When `true`, extracts and removes reasoning tags from content but discards the reasoning text. Useful when you want to clean reasoning tags from output without storing the reasoning content. |
| `reasoning.thinking_start_tokens` | array | `[]` | List of custom thinking start tokens to detect in prompts. Custom tokens are checked before default tokens. |
| `reasoning.tag_pairs` | array | `[]` | List of custom tag pairs for reasoning extraction. Each entry has `start` and `end` fields. Custom pairs are checked before default pairs. |

### Reasoning Tag Formats

The reasoning extraction supports multiple tag formats used by different models:

- `<thinking>...</thinking>` - General thinking tag
- `<think>...</think>` - DeepSeek, Granite, ExaOne, GLM models
- `<|START_THINKING|>...<|END_THINKING|>` - Command-R models
- `<|inner_prefix|>...<|inner_suffix|>` - Apertus models
- `<seed:think>...</seed:think>` - Seed models
- `<|think|>...<|end|><|begin|>assistant<|content|>` - Solar Open models
- `[THINK]...[/THINK]` - Magistral models

### Examples

**Disable reasoning extraction:**
```yaml
reasoning:
  disable: true
```

**Extract reasoning but don't prepend tags:**
```yaml
reasoning:
  disable_reasoning_tag_prefill: true
```

**Strip reasoning tags without storing reasoning content:**
```yaml
reasoning:
  strip_reasoning_only: true
```

**Complete example with reasoning configuration:**
```yaml
name: deepseek-model
backend: llama-cpp
parameters:
  model: deepseek.gguf

reasoning:
  disable: false
  disable_reasoning_tag_prefill: false
  strip_reasoning_only: false
```

**Example with custom tokens and tag pairs:**
```yaml
name: custom-reasoning-model
backend: llama-cpp
parameters:
  model: custom.gguf

reasoning:
  thinking_start_tokens:
    - "<custom:think>"
    - "<my:reasoning>"
  tag_pairs:
    - start: "<custom:think>"
      end: "</custom:think>"
    - start: "<my:reasoning>"
      end: "</my:reasoning>"
```

**Note:** Custom tokens and tag pairs are checked before the default ones, giving them priority. This allows you to override default behavior or add support for new reasoning tag formats.

### Per-Request Override via Metadata

The `reasoning.disable` setting from model configuration can be overridden on a per-request basis using the `metadata` field in the OpenAI chat completion request. This allows you to enable or disable thinking for individual requests without changing the model configuration.

The `metadata` field accepts a `map[string]string` that is forwarded to the backend. The `enable_thinking` key controls thinking behavior:

```bash
# Enable thinking for a single request (overrides model config)
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3",
    "messages": [{"role": "user", "content": "Explain quantum computing"}],
    "metadata": {"enable_thinking": "true"}
  }'

# Disable thinking for a single request (overrides model config)
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3",
    "messages": [{"role": "user", "content": "Hello"}],
    "metadata": {"enable_thinking": "false"}
  }'
```

**Priority order:**
1. Request-level `metadata.enable_thinking` (highest priority)
2. Model config `reasoning.disable` (fallback)
3. Auto-detected from model template (default)

## Pipeline Configuration

Define pipelines for audio-to-audio processing and the [Realtime API]({{%relref "features/openai-realtime" %}}):

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


### GPU Auto-Fit Mode

**Note**: By default, LocalAI sets `gpu_layers` to a very large value (9999999), which effectively disables llama-cpp's auto-fit functionality. This is intentional to work with LocalAI's VRAM-based model unloading mechanism.

To enable llama-cpp's auto-fit mode, set `gpu_layers: -1` in your model configuration. However, be aware of the following:

1. **Trade-off**: Enabling auto-fit conflicts with LocalAI's built-in VRAM threshold-based unloading. Auto-fit attempts to fit all tensors into GPU memory automatically, while LocalAI's unloading mechanism removes models when VRAM usage exceeds thresholds.

2. **Known Issues**: Setting `gpu_layers: -1` may trigger `tensor_buft_override` buffer errors in some configurations, particularly when the model exceeds available GPU memory.

3. **Recommendation**: 
   - Use the default settings for most use cases (LocalAI manages VRAM automatically)
   - Only enable `gpu_layers: -1` if you understand the implications and have tested on your specific hardware
   - Monitor VRAM usage carefully when using auto-fit mode

This is a known limitation being tracked in issue [#8562](https://github.com/mudler/LocalAI/issues/8562). A future implementation may provide a runtime toggle or custom logic to reconcile auto-fit with threshold-based unloading.
