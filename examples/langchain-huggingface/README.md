# Data query example

Example of integration with HuggingFace Inference API with help of [langchaingo](https://github.com/tmc/langchaingo).

## Setup

Download the LocalAI and start the API:

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/langchain-huggingface

docker-compose up -d
```

Node: Ensure you've set `HUGGINGFACEHUB_API_TOKEN` environment variable, you can generate it
on [Settings / Access Tokens](https://huggingface.co/settings/tokens) page of HuggingFace site.

This is an example `.env` file for LocalAI:

```ini
MODELS_PATH=/models
CONTEXT_SIZE=512
HUGGINGFACEHUB_API_TOKEN=hg_123456
```

## Using remote models

Now you can use any remote models available via HuggingFace API, for example let's enable using of
[gpt2](https://huggingface.co/gpt2) model in `gpt-3.5-turbo.yaml` config:

```yml
name: gpt-3.5-turbo
parameters:
  model: gpt2
  top_k: 80
  temperature: 0.2
  top_p: 0.7
context_size: 1024
backend: "langchain-huggingface"
stopwords:
- "HUMAN:"
- "GPT:"
roles:
  user: " "
  system: " "
template:
  completion: completion
  chat: gpt4all
```

Here is you can see in field `parameters.model` equal `gpt2` and `backend` equal `langchain-huggingface`.

## How to use

```shell
# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"gpt-3.5-turbo","object":"model"}]}

curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
  "model": "gpt-3.5-turbo",
  "prompt": "A long time ago in a galaxy far, far away",
  "temperature": 0.7
}'
```