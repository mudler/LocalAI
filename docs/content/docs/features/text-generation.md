
+++
disableToc = false
title = "📖 Text generation (GPT)"
weight = 2
+++

LocalAI supports generating text with GPT with `llama.cpp` and other backends (such as `rwkv.cpp` as ) see also the [Model compatibility]({{%relref "docs/model-compatibility" %}}) for an up-to-date list of the supported model families.

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

### AutoGPTQ

[AutoGPTQ](https://github.com/PanQiWei/AutoGPTQ) is an easy-to-use LLMs quantization package with user-friendly apis, based on GPTQ algorithm.

#### Prerequisites

This is an extra backend - in the container images is already available and there is nothing to do for the setup.

If you are building LocalAI locally, you need to install [AutoGPTQ manually](https://github.com/PanQiWei/AutoGPTQ#quick-installation).


#### Model setup

The models are automatically downloaded from `huggingface` if not present the first time. It is possible to define models via `YAML` config file, or just by querying the endpoint with the `huggingface` repository model name. For example, create a `YAML` config file in `models/`:

```
name: orca
backend: autogptq
model_base_name: "orca_mini_v2_13b-GPTQ-4bit-128g.no-act.order"
parameters:
  model: "TheBloke/orca_mini_v2_13b-GPTQ"
# ...
```

Test with:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{                                                                                                         
   "model": "orca",
   "messages": [{"role": "user", "content": "How are you?"}],
   "temperature": 0.1
 }'
```
### RWKV

A full example on how to run a rwkv model is in the [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/rwkv).

Note: rwkv models needs to specify the backend `rwkv` in the YAML config files and have an associated tokenizer along that needs to be provided with it:

```
36464540 -rw-r--r--  1 mudler mudler 1.2G May  3 10:51 rwkv_small
36464543 -rw-r--r--  1 mudler mudler 2.4M May  3 10:51 rwkv_small.tokenizer.json
```

### llama.cpp

[llama.cpp](https://github.com/ggerganov/llama.cpp) is a popular port of Facebook's LLaMA model in C/C++.

{{% alert note %}}

The `ggml` file format has been deprecated. If you are using `ggml` models and you are configuring your model with a YAML file, specify, use the `llama-ggml` backend instead. If you are relying in automatic detection of the model, you should be fine. For `gguf` models, use the `llama` backend. The go backend is deprecated as well but still available as `go-llama`. The go backend supports still features not available in the mainline: speculative sampling and embeddings.

{{% /alert %}}

#### Features

The `llama.cpp` model supports the following features:
- [📖 Text generation (GPT)]({{%relref "docs/features/text-generation" %}})
- [🧠 Embeddings]({{%relref "docs/features/embeddings" %}})
- [🔥 OpenAI functions]({{%relref "docs/features/openai-functions" %}})
- [✍️ Constrained grammars]({{%relref "docs/features/constrained_grammars" %}})

#### Setup

LocalAI supports `llama.cpp` models out of the box. You can use the `llama.cpp` model in the same way as any other model. 

##### Manual setup

It is sufficient to copy the `ggml` or `gguf` model files in the `models` folder. You can refer to the model in the `model` parameter in the API calls.

[You can optionally create an associated YAML]({{%relref "docs/advanced" %}}) model config file to tune the model's parameters or apply a template to the prompt.

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

Models can be also preloaded or downloaded on demand. To learn about model galleries, check out the [model gallery documentation]({{%relref "docs/features/model-gallery" %}}).

#### YAML configuration

To use the `llama.cpp` backend, specify `llama` as the backend in the YAML file:

```yaml
name: llama
backend: llama
parameters:
  # Relative to the models path
  model: file.gguf.bin
```

In the example above we specify `llama` as the backend to restrict loading `gguf` models only. 

For instance, to use the `llama-ggml` backend for `ggml` models:

```yaml
name: llama
backend: llama-ggml
parameters:
  # Relative to the models path
  model: file.ggml.bin
```

#### Reference

- [llama](https://github.com/ggerganov/llama.cpp)
- [binding](https://github.com/go-skynet/go-llama.cpp)