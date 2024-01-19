
+++
disableToc = false
title = "🧠 Embeddings"
weight = 13
url = "/features/embeddings/"
+++

LocalAI supports generating embeddings for text or list of tokens.

For the API documentation you can refer to the OpenAI docs: https://platform.openai.com/docs/api-reference/embeddings

## Model compatibility

The embedding endpoint is compatible with `llama.cpp` models, `bert.cpp` models and sentence-transformers models available in huggingface.

## Manual Setup

Create a `YAML` config file in the `models` directory. Specify the `backend` and the model file.

```yaml
name: text-embedding-ada-002 # The model name used in the API
parameters:
  model: <model_file>
backend: "<backend>"
embeddings: true
# .. other parameters
```

## Bert embeddings

To use `bert.cpp` models you can use the `bert` embedding backend.

An example model config file:

```yaml
name: text-embedding-ada-002
parameters:
  model: bert
backend: bert-embeddings
embeddings: true
# .. other parameters
```

The `bert` backend uses [bert.cpp](https://github.com/skeskinen/bert.cpp) and uses `ggml` models.

For instance you can download the `ggml` quantized version of `all-MiniLM-L6-v2` from https://huggingface.co/skeskinen/ggml:

```bash
wget https://huggingface.co/skeskinen/ggml/resolve/main/all-MiniLM-L6-v2/ggml-model-q4_0.bin -O models/bert
```

To test locally (LocalAI server running on `localhost`),
you can use `curl` (and `jq` at the end to prettify):

```bash
curl http://localhost:8080/embeddings -X POST -H "Content-Type: application/json" -d '{
  "input": "Your text string goes here",
  "model": "text-embedding-ada-002"
}' | jq "."
```

## Huggingface embeddings

To use `sentence-transformers` and models in `huggingface` you can use the `sentencetransformers` embedding backend.

```yaml
name: text-embedding-ada-002
backend: sentencetransformers
embeddings: true
parameters:
  model: all-MiniLM-L6-v2
```

The `sentencetransformers` backend uses Python [sentence-transformers](https://github.com/UKPLab/sentence-transformers). For a list of all pre-trained models available see here: https://github.com/UKPLab/sentence-transformers#pre-trained-models

{{% alert note %}}

- The `sentencetransformers` backend is an optional backend of LocalAI and uses Python. If you are running `LocalAI` from the containers you are good to go and should be already configured for use.
- If you are running `LocalAI` manually you must install the python dependencies (`make prepare-extra-conda-environments`). This requires `conda` to be installed.
- For local execution, you also have to specify the extra backend in the `EXTERNAL_GRPC_BACKENDS` environment variable.
    - Example: `EXTERNAL_GRPC_BACKENDS="sentencetransformers:/path/to/LocalAI/backend/python/sentencetransformers/sentencetransformers.py"`
- The `sentencetransformers` backend does support only embeddings of text, and not of tokens. If you need to embed tokens you can use the `bert` backend or `llama.cpp`.
- No models are required to be downloaded before using the `sentencetransformers` backend. The models will be downloaded automatically the first time the API is used.

{{% /alert %}}

## Llama.cpp embeddings

Embeddings with `llama.cpp` are supported with the `llama` backend.

```yaml
name: my-awesome-model
backend: llama
embeddings: true
parameters:
  model: ggml-file.bin
# ...
```

## 💡 Examples

- Example that uses LLamaIndex and LocalAI as embedding: [here](https://github.com/go-skynet/LocalAI/tree/master/examples/query_data/).
