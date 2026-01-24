
+++
disableToc = false
title = "📖 Text generation (GPT)"
weight = 10
url = "/features/text-generation/"
+++

LocalAI supports generating text with GPT with `llama.cpp` and other backends (such as `rwkv.cpp` as ) see also the [Model compatibility]({{%relref "reference/compatibility-table" %}}) for an up-to-date list of the supported model families.

Note:

- You can also specify the model name as part of the OpenAI token.
- If only one model is available, the API will use it for all the requests.

## API Reference

### Chat completions

https://platform.openai.com/docs/api-reference/chat

For example, to generate a chat completion, you can send a POST request to the `/v1/chat/completions` endpoint with the instruction as the request body:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "ggml-koala-7b-model-q4_0-r2.bin",
  "messages": [{"role": "user", "content": "Say this is a test!"}],
  "temperature": 0.7
}'
```

Available additional parameters: `top_p`, `top_k`, `max_tokens`

### Edit completions

https://platform.openai.com/docs/api-reference/edits

To generate an edit completion you can send a POST request to the `/v1/edits` endpoint with the instruction as the request body:

```bash
curl http://localhost:8080/v1/edits -H "Content-Type: application/json" -d '{
  "model": "ggml-koala-7b-model-q4_0-r2.bin",
  "instruction": "rephrase",
  "input": "Black cat jumped out of the window",
  "temperature": 0.7
}'
```

Available additional parameters: `top_p`, `top_k`, `max_tokens`.

### Completions

https://platform.openai.com/docs/api-reference/completions

To generate a completion, you can send a POST request to the `/v1/completions` endpoint with the instruction as per the request body:

```bash
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
  "model": "ggml-koala-7b-model-q4_0-r2.bin",
  "prompt": "A long time ago in a galaxy far, far away",
  "temperature": 0.7
}'
```

Available additional parameters: `top_p`, `top_k`, `max_tokens`

### List models

You can list all the models available with:

```bash
curl http://localhost:8080/v1/models
```

### Anthropic Messages API

LocalAI supports the Anthropic Messages API, which is compatible with Claude clients. This endpoint provides a structured way to send messages and receive responses, with support for tools, streaming, and multimodal content.

**Endpoint:** `POST /v1/messages` or `POST /messages`

**Reference:** https://docs.anthropic.com/claude/reference/messages_post

#### Basic Usage

```bash
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "ggml-koala-7b-model-q4_0-r2.bin",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "Say this is a test!"}
    ]
  }'
```

#### Request Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `model` | string | Yes | The model identifier |
| `messages` | array | Yes | Array of message objects with `role` and `content` |
| `max_tokens` | integer | Yes | Maximum number of tokens to generate (must be > 0) |
| `system` | string | No | System message to set the assistant's behavior |
| `temperature` | float | No | Sampling temperature (0.0 to 1.0) |
| `top_p` | float | No | Nucleus sampling parameter |
| `top_k` | integer | No | Top-k sampling parameter |
| `stop_sequences` | array | No | Array of strings that will stop generation |
| `stream` | boolean | No | Enable streaming responses |
| `tools` | array | No | Array of tool definitions for function calling |
| `tool_choice` | string/object | No | Tool choice strategy: "auto", "any", "none", or specific tool |
| `metadata` | object | No | Custom metadata to attach to the request |

#### Message Format

Messages can contain text or structured content blocks:

```bash
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "ggml-koala-7b-model-q4_0-r2.bin",
    "max_tokens": 1024,
    "messages": [
      {
        "role": "user",
        "content": [
          {
            "type": "text",
            "text": "What is in this image?"
          },
          {
            "type": "image",
            "source": {
              "type": "base64",
              "media_type": "image/jpeg",
              "data": "base64_encoded_image_data"
            }
          }
        ]
      }
    ]
  }'
```

#### Tool Calling

The Anthropic API supports function calling through tools:

```bash
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "ggml-koala-7b-model-q4_0-r2.bin",
    "max_tokens": 1024,
    "tools": [
      {
        "name": "get_weather",
        "description": "Get the current weather",
        "input_schema": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "The city and state"
            }
          },
          "required": ["location"]
        }
      }
    ],
    "tool_choice": "auto",
    "messages": [
      {"role": "user", "content": "What is the weather in San Francisco?"}
    ]
  }'
```

#### Streaming

Enable streaming responses by setting `stream: true`:

```bash
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "ggml-koala-7b-model-q4_0-r2.bin",
    "max_tokens": 1024,
    "stream": true,
    "messages": [
      {"role": "user", "content": "Tell me a story"}
    ]
  }'
```

Streaming responses use Server-Sent Events (SSE) format with event types: `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, and `message_stop`.

#### Response Format

```json
{
  "id": "msg_abc123",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "This is a test!"
    }
  ],
  "model": "ggml-koala-7b-model-q4_0-r2.bin",
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 10,
    "output_tokens": 5
  }
}
```

### Open Responses API

LocalAI supports the Open Responses API specification, which provides a standardized interface for AI model interactions with support for background processing, streaming, tool calling, and advanced features like reasoning.

**Endpoint:** `POST /v1/responses` or `POST /responses`

**Reference:** https://www.openresponses.org/specification

#### Basic Usage

```bash
curl http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "ggml-koala-7b-model-q4_0-r2.bin",
    "input": "Say this is a test!",
    "max_output_tokens": 1024
  }'
```

#### Request Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `model` | string | Yes | The model identifier |
| `input` | string/array | Yes | Input text or array of input items |
| `max_output_tokens` | integer | No | Maximum number of tokens to generate |
| `temperature` | float | No | Sampling temperature |
| `top_p` | float | No | Nucleus sampling parameter |
| `instructions` | string | No | System instructions |
| `tools` | array | No | Array of tool definitions |
| `tool_choice` | string/object | No | Tool choice: "auto", "required", "none", or specific tool |
| `stream` | boolean | No | Enable streaming responses |
| `background` | boolean | No | Run request in background (returns immediately) |
| `store` | boolean | No | Whether to store the response |
| `reasoning` | object | No | Reasoning configuration with `effort` and `summary` |
| `parallel_tool_calls` | boolean | No | Allow parallel tool calls |
| `max_tool_calls` | integer | No | Maximum number of tool calls |
| `presence_penalty` | float | No | Presence penalty (-2.0 to 2.0) |
| `frequency_penalty` | float | No | Frequency penalty (-2.0 to 2.0) |
| `top_logprobs` | integer | No | Number of top logprobs to return |
| `truncation` | string | No | Truncation mode: "auto" or "disabled" |
| `text_format` | object | No | Text format configuration |
| `metadata` | object | No | Custom metadata |

#### Input Format

Input can be a simple string or an array of structured items:

```bash
curl http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "ggml-koala-7b-model-q4_0-r2.bin",
    "input": [
      {
        "type": "message",
        "role": "user",
        "content": "What is the weather?"
      }
    ],
    "max_output_tokens": 1024
  }'
```

#### Background Processing

Run requests in the background for long-running tasks:

```bash
curl http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "ggml-koala-7b-model-q4_0-r2.bin",
    "input": "Generate a long story",
    "max_output_tokens": 4096,
    "background": true
  }'
```

The response will include a response ID that can be used to poll for completion:

```json
{
  "id": "resp_abc123",
  "object": "response",
  "status": "in_progress",
  "created_at": 1234567890
}
```

#### Retrieving Background Responses

Use the GET endpoint to retrieve background responses:

```bash
# Get response by ID
curl http://localhost:8080/v1/responses/resp_abc123

# Resume streaming with query parameters
curl "http://localhost:8080/v1/responses/resp_abc123?stream=true&starting_after=10"
```

#### Canceling Background Responses

Cancel a background response that's still in progress:

```bash
curl -X POST http://localhost:8080/v1/responses/resp_abc123/cancel
```

#### Tool Calling

Open Responses API supports function calling with tools:

```bash
curl http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "ggml-koala-7b-model-q4_0-r2.bin",
    "input": "What is the weather in San Francisco?",
    "tools": [
      {
        "type": "function",
        "name": "get_weather",
        "description": "Get the current weather",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "The city and state"
            }
          },
          "required": ["location"]
        }
      }
    ],
    "tool_choice": "auto",
    "max_output_tokens": 1024
  }'
```

#### Reasoning Configuration

Configure reasoning effort and summary style:

```bash
curl http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "ggml-koala-7b-model-q4_0-r2.bin",
    "input": "Solve this complex problem step by step",
    "reasoning": {
      "effort": "high",
      "summary": "detailed"
    },
    "max_output_tokens": 2048
  }'
```

#### Response Format

```json
{
  "id": "resp_abc123",
  "object": "response",
  "created_at": 1234567890,
  "completed_at": 1234567895,
  "status": "completed",
  "model": "ggml-koala-7b-model-q4_0-r2.bin",
  "output": [
    {
      "type": "message",
      "id": "msg_001",
      "role": "assistant",
      "content": [
        {
          "type": "output_text",
          "text": "This is a test!",
          "annotations": [],
          "logprobs": []
        }
      ],
      "status": "completed"
    }
  ],
  "error": null,
  "incomplete_details": null,
  "temperature": 0.7,
  "top_p": 1.0,
  "presence_penalty": 0.0,
  "frequency_penalty": 0.0,
  "usage": {
    "input_tokens": 10,
    "output_tokens": 5,
    "total_tokens": 15,
    "input_tokens_details": {
      "cached_tokens": 0
    },
    "output_tokens_details": {
      "reasoning_tokens": 0
    }
  }
}
```

## Backends

### RWKV

RWKV support is available through llama.cpp (see below)

### llama.cpp

[llama.cpp](https://github.com/ggerganov/llama.cpp) is a popular port of Facebook's LLaMA model in C/C++.

{{% notice note %}}

The `ggml` file format has been deprecated. If you are using `ggml` models and you are configuring your model with a YAML file, specify, use a LocalAI version older than v2.25.0. For `gguf` models, use the `llama` backend. The go backend is deprecated as well but still available as `go-llama`.

 {{% /notice %}}

#### Features

The `llama.cpp` model supports the following features:
- [📖 Text generation (GPT)]({{%relref "features/text-generation" %}})
- [🧠 Embeddings]({{%relref "features/embeddings" %}})
- [🔥 OpenAI functions]({{%relref "features/openai-functions" %}})
- [✍️ Constrained grammars]({{%relref "features/constrained_grammars" %}})

#### Setup

LocalAI supports `llama.cpp` models out of the box. You can use the `llama.cpp` model in the same way as any other model. 

##### Manual setup

It is sufficient to copy the `ggml` or `gguf` model files in the `models` folder. You can refer to the model in the `model` parameter in the API calls.

[You can optionally create an associated YAML]({{%relref "advanced" %}}) model config file to tune the model's parameters or apply a template to the prompt.

Prompt templates are useful for models that are fine-tuned towards a specific prompt. 

##### Automatic setup

LocalAI supports model galleries which are indexes of models. For instance, the huggingface gallery contains a large curated index of models from the huggingface model hub for `ggml` or `gguf` models.

For instance, if you have the galleries enabled and LocalAI already running, you can just start chatting with models in huggingface by running:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "TheBloke/WizardLM-13B-V1.2-GGML/wizardlm-13b-v1.2.ggmlv3.q2_K.bin",
     "messages": [{"role": "user", "content": "Say this is a test!"}],
     "temperature": 0.1
   }'
```

LocalAI will automatically download and configure the model in the `model` directory.

Models can be also preloaded or downloaded on demand. To learn about model galleries, check out the [model gallery documentation]({{%relref "features/model-gallery" %}}).

#### YAML configuration

To use the `llama.cpp` backend, specify `llama-cpp` as the backend in the YAML file:

```yaml
name: llama
backend: llama-cpp
parameters:
  # Relative to the models path
  model: file.gguf
```

#### Backend Options

The `llama.cpp` backend supports additional configuration options that can be specified in the `options` field of your model YAML configuration. These options allow fine-tuning of the backend behavior:

| Option | Type | Description | Example |
|--------|------|-------------|---------|
| `use_jinja` or `jinja` | boolean | Enable Jinja2 template processing for chat templates. When enabled, the backend uses Jinja2-based chat templates from the model for formatting messages. | `use_jinja:true` |
| `context_shift` | boolean | Enable context shifting, which allows the model to dynamically adjust context window usage. | `context_shift:true` |
| `cache_ram` | integer | Set the maximum RAM cache size in MiB for KV cache. Use `-1` for unlimited (default). | `cache_ram:2048` |
| `parallel` or `n_parallel` | integer | Enable parallel request processing. When set to a value greater than 1, enables continuous batching for handling multiple requests concurrently. | `parallel:4` |
| `grpc_servers` or `rpc_servers` | string | Comma-separated list of gRPC server addresses for distributed inference. Allows distributing workload across multiple llama.cpp workers. | `grpc_servers:localhost:50051,localhost:50052` |
| `fit_params` or `fit` | boolean | Enable auto-adjustment of model/context parameters to fit available device memory. Default: `true`. | `fit_params:true` |
| `fit_params_target` or `fit_target` | integer | Target margin per device in MiB when using fit_params. Default: `1024` (1GB). | `fit_target:2048` |
| `fit_params_min_ctx` or `fit_ctx` | integer | Minimum context size that can be set by fit_params. Default: `4096`. | `fit_ctx:2048` |
| `n_cache_reuse` or `cache_reuse` | integer | Minimum chunk size to attempt reusing from the cache via KV shifting. Default: `0` (disabled). | `cache_reuse:256` |
| `slot_prompt_similarity` or `sps` | float | How much the prompt of a request must match the prompt of a slot to use that slot. Default: `0.1`. Set to `0` to disable. | `sps:0.5` |
| `swa_full` | boolean | Use full-size SWA (Sliding Window Attention) cache. Default: `false`. | `swa_full:true` |
| `cont_batching` or `continuous_batching` | boolean | Enable continuous batching for handling multiple sequences. Default: `true`. | `cont_batching:true` |
| `check_tensors` | boolean | Validate tensor data for invalid values during model loading. Default: `false`. | `check_tensors:true` |
| `warmup` | boolean | Enable warmup run after model loading. Default: `true`. | `warmup:false` |
| `no_op_offload` | boolean | Disable offloading host tensor operations to device. Default: `false`. | `no_op_offload:true` |
| `kv_unified` or `unified_kv` | boolean | Enable unified KV cache. Default: `false`. | `kv_unified:true` |
| `n_ctx_checkpoints` or `ctx_checkpoints` | integer | Maximum number of context checkpoints per slot. Default: `8`. | `ctx_checkpoints:4` |

**Example configuration with options:**

```yaml
name: llama-model
backend: llama
parameters:
  model: model.gguf
options:
  - use_jinja:true
  - context_shift:true
  - cache_ram:4096
  - parallel:2
  - fit_params:true
  - fit_target:1024
  - slot_prompt_similarity:0.5
```

**Note:** The `parallel` option can also be set via the `LLAMACPP_PARALLEL` environment variable, and `grpc_servers` can be set via the `LLAMACPP_GRPC_SERVERS` environment variable. Options specified in the YAML file take precedence over environment variables.

#### Reference

- [llama](https://github.com/ggerganov/llama.cpp)


### vLLM

[vLLM](https://github.com/vllm-project/vllm) is a fast and easy-to-use library for LLM inference.

LocalAI has a built-in integration with vLLM, and it can be used to run models. You can check out `vllm` performance [here](https://github.com/vllm-project/vllm#performance).

#### Setup

Create a YAML file for the model you want to use with `vllm`.

To setup a model, you need to just specify the model name in the YAML config file:
```yaml
name: vllm
backend: vllm
parameters:
    model: "facebook/opt-125m"

```

The backend will automatically download the required files in order to run the model.


#### Usage

Use the `completions` endpoint by specifying the `vllm` backend:
```
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{   
   "model": "vllm",
   "prompt": "Hello, my name is",
   "temperature": 0.1, "top_p": 0.1
 }'
```

### Transformers

[Transformers](https://huggingface.co/docs/transformers/index) is a State-of-the-art Machine Learning library for PyTorch, TensorFlow, and JAX.

LocalAI has a built-in integration with Transformers, and it can be used to run models.

This is an extra backend - in the container images (the `extra` images already contains python dependencies for Transformers) is already available and there is nothing to do for the setup.

#### Setup

Create a YAML file for the model you want to use with `transformers`.

To setup a model, you need to just specify the model name in the YAML config file:
```yaml
name: transformers
backend: transformers
parameters:
    model: "facebook/opt-125m"
type: AutoModelForCausalLM
quantization: bnb_4bit # One of: bnb_8bit, bnb_4bit, xpu_4bit, xpu_8bit (optional)
```

The backend will automatically download the required files in order to run the model.

#### Parameters

##### Type

| Type | Description |
| --- | --- |
| `AutoModelForCausalLM` | `AutoModelForCausalLM` is a model that can be used to generate sequences. Use it for NVIDIA CUDA and Intel GPU with Intel Extensions for Pytorch acceleration |
| `OVModelForCausalLM` | for Intel CPU/GPU/NPU OpenVINO Text Generation models |
| `OVModelForFeatureExtraction` | for Intel CPU/GPU/NPU OpenVINO Embedding acceleration |
| N/A | Defaults to `AutoModel` |

- `OVModelForCausalLM` requires OpenVINO IR [Text Generation](https://huggingface.co/models?library=openvino&pipeline_tag=text-generation) models from Hugging face
- `OVModelForFeatureExtraction` works with any Safetensors Transformer [Feature Extraction](https://huggingface.co/models?pipeline_tag=feature-extraction&library=transformers,safetensors) model from Huggingface (Embedding Model)

Please note that streaming is currently not implemente in `AutoModelForCausalLM` for Intel GPU.
AMD GPU support is not implemented.
Although AMD CPU is not officially supported by OpenVINO there are reports that it works: YMMV.

##### Embeddings
Use `embeddings: true` if the model is an embedding model

##### Inference device selection
Transformer backend tries to automatically select the best device for inference, anyway you can override the decision manually overriding with the `main_gpu` parameter.

| Inference Engine | Applicable Values |
| --- | --- |
| CUDA | `cuda`, `cuda.X` where X is the GPU device like in `nvidia-smi -L` output |
| OpenVINO | Any applicable value from [Inference Modes](https://docs.openvino.ai/2024/openvino-workflow/running-inference/inference-devices-and-modes.html) like `AUTO`,`CPU`,`GPU`,`NPU`,`MULTI`,`HETERO` |

Example for CUDA:
`main_gpu: cuda.0`

Example for OpenVINO:
`main_gpu: AUTO:-CPU`

This parameter applies to both Text Generation and Feature Extraction (i.e. Embeddings) models.

##### Inference Precision
Transformer backend automatically select the fastest applicable inference precision according to the device support.
CUDA backend can manually enable *bfloat16* if your hardware support it with the following parameter:

`f16: true`

##### Quantization

| Quantization | Description |
| --- | --- |
| `bnb_8bit` | 8-bit quantization |
| `bnb_4bit` | 4-bit quantization |
| `xpu_8bit` | 8-bit quantization for Intel XPUs |
| `xpu_4bit` | 4-bit quantization for Intel XPUs |

##### Trust Remote Code
Some models like Microsoft Phi-3 requires external code than what is provided by the transformer library.
By default it is disabled for security.
It can be manually enabled with:
`trust_remote_code: true`

##### Maximum Context Size
Maximum context size in bytes can be specified with the parameter: `context_size`. Do not use values higher than what your model support.

Usage example:
`context_size: 8192`

##### Auto Prompt Template
Usually chat template is defined by the model author in the `tokenizer_config.json` file.
To enable it use the `use_tokenizer_template: true` parameter in the `template` section.

Usage example:
```
template:
  use_tokenizer_template: true
```

##### Custom Stop Words
Stopwords are usually defined in `tokenizer_config.json` file.
They can be overridden with the `stopwords` parameter in case of need like in llama3-Instruct model.

Usage example:
```
stopwords:
- "<|eot_id|>"
- "<|end_of_text|>"
```

#### Usage

Use the `completions` endpoint by specifying the `transformers` model:
```
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{   
   "model": "transformers",
   "prompt": "Hello, my name is",
   "temperature": 0.1, "top_p": 0.1
 }'
```

#### Examples

##### OpenVINO

A model configuration file for openvion and starling model:

```yaml
name: starling-openvino
backend: transformers
parameters:
  model: fakezeta/Starling-LM-7B-beta-openvino-int8
context_size: 8192
threads: 6
f16: true
type: OVModelForCausalLM
stopwords:
- <|end_of_turn|>
- <|endoftext|>
prompt_cache_path: "cache"
prompt_cache_all: true
template:
  chat_message: |
    {{if eq .RoleName "system"}}{{.Content}}<|end_of_turn|>{{end}}{{if eq .RoleName "assistant"}}<|end_of_turn|>GPT4 Correct Assistant: {{.Content}}<|end_of_turn|>{{end}}{{if eq .RoleName "user"}}GPT4 Correct User: {{.Content}}{{end}}

  chat: |
    {{.Input}}<|end_of_turn|>GPT4 Correct Assistant:

  completion: |
    {{.Input}}
```