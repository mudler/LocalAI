
+++
disableToc = false
title = "📖 Text generation (GPT)"
weight = 2
+++

LocalAI supports generating text with GPT with `llama.cpp` and other backends (such as `rwkv.cpp` as ) see also the [Model compatibility]({{%relref "model-compatibility" %}}) for an up-to-date list of the supported model families.

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
