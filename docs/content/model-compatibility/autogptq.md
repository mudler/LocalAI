
+++
disableToc = false
title = "🦙 AutoGPTQ"
weight = 3
+++

[AutoGPTQ](https://github.com/PanQiWei/AutoGPTQ) is an easy-to-use LLMs quantization package with user-friendly apis, based on GPTQ algorithm.

## Prerequisites

This is an extra backend - in the container images is already available and there is nothing to do for the setup.

If you are building LocalAI locally, you need to install [AutoGPTQ manually](https://github.com/PanQiWei/AutoGPTQ#quick-installation).


## Model setup

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
