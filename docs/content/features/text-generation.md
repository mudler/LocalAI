
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
```

**Note:** The `parallel` option can also be set via the `LLAMACPP_PARALLEL` environment variable, and `grpc_servers` can be set via the `LLAMACPP_GRPC_SERVERS` environment variable. Options specified in the YAML file take precedence over environment variables.

#### Reference

- [llama](https://github.com/ggerganov/llama.cpp)


### exllama/2

[Exllama](https://github.com/turboderp/exllama) is a "A more memory-efficient rewrite of the HF transformers implementation of Llama for use with quantized weights". Both `exllama` and `exllama2` are supported.

#### Model setup

Download the model as a folder inside the `model ` directory and create a YAML file specifying the `exllama` backend. For instance with the `TheBloke/WizardLM-7B-uncensored-GPTQ` model:

```
$ git lfs install
$ cd models && git clone https://huggingface.co/TheBloke/WizardLM-7B-uncensored-GPTQ
$ ls models/                                                                 
.keep                        WizardLM-7B-uncensored-GPTQ/ exllama.yaml
$ cat models/exllama.yaml                                                     
name: exllama
parameters:
  model: WizardLM-7B-uncensored-GPTQ
backend: exllama
```

Test with:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{                                                                                                         
   "model": "exllama",
   "messages": [{"role": "user", "content": "How are you?"}],
   "temperature": 0.1
 }'
```

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