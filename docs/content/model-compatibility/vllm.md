
+++
disableToc = false
title = "vLLM"
weight = 4
+++

[vLLM](https://github.com/vllm-project/vllm) is a fast and easy-to-use library for LLM inference.

LocalAI has a built-in integration with vLLM, and it can be used to run models. You can check out `vllm` performance [here](https://github.com/vllm-project/vllm#performance).

## Setup

Create a YAML file for the model you want to use with `vllm`.

To setup a model, you need to just specify the model name in the YAML config file:
```yaml
name: vllm
backend: vllm
parameters:
    model: "facebook/opt-125m"

# Decomment to specify a quantization method (optional)
# quantization: "awq"
```

The backend will automatically download the required files in order to run the model.


## Usage

Use the `completions` endpoint by specifying the `vllm` backend:
```
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{   
   "model": "vllm",
   "prompt": "Hello, my name is",
   "temperature": 0.1, "top_p": 0.1
 }'
 ```