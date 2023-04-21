<h1 align="center">
  <br>
  <img height="300" src="https://user-images.githubusercontent.com/2420543/233147843-88697415-6dbf-4368-a862-ab217f9f7342.jpeg"> <br>
    LocalAI
<br>
</h1>

> :warning: This project has been renamed from `llama-cli` to `LocalAI` to reflect the fact that we are focusing on a fast drop-in OpenAI API rather on the CLI interface. We think that there are already many projects that can be used as a CLI interface already, for instance  [llama.cpp](https://github.com/ggerganov/llama.cpp) and [gpt4all](https://github.com/nomic-ai/gpt4all). If you are were using `llama-cli` for CLI interactions and want to keep using it, use older versions or please open up an issue - contributions are welcome!

LocalAI is a straightforward, drop-in replacement API compatible with OpenAI for local CPU inferencing, based on [llama.cpp](https://github.com/ggerganov/llama.cpp), [gpt4all](https://github.com/nomic-ai/gpt4all) and [ggml](https://github.com/ggerganov/ggml), including support GPT4ALL-J which is Apache 2.0 Licensed and can be used for commercial purposes.

- OpenAI compatible API
- Supports multiple-models
- Once loaded the first time, it keep models loaded in memory for faster inference
- Support for prompt templates
- Doesn't shell-out, but uses C bindings for a faster inference and better performance. Uses [go-llama.cpp](https://github.com/go-skynet/go-llama.cpp) and [go-gpt4all-j.cpp](https://github.com/go-skynet/go-gpt4all-j.cpp).

## Model compatibility

It is compatible with the models supported by [llama.cpp](https://github.com/ggerganov/llama.cpp) supports also [GPT4ALL-J](https://github.com/nomic-ai/gpt4all) and [cerebras-GPT with ggml](https://huggingface.co/lxe/Cerebras-GPT-2.7B-Alpaca-SP-ggml).

Tested with:
- Vicuna
- Alpaca
- [GPT4ALL](https://github.com/nomic-ai/gpt4all)
- [GPT4ALL-J](https://gpt4all.io/models/ggml-gpt4all-j.bin)
- Koala
- [cerebras-GPT with ggml](https://huggingface.co/lxe/Cerebras-GPT-2.7B-Alpaca-SP-ggml)

It should also be compatible with StableLM and GPTNeoX ggml models (untested)

Note: You might need to convert older models to the new format, see [here](https://github.com/ggerganov/llama.cpp#using-gpt4all) for instance to run `gpt4all`.

## Usage

> `LocalAI` comes by default as a container image. You can check out all the available images with corresponding tags [here](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest).

The easiest way to run LocalAI is by using `docker-compose`:

```bash

git clone https://github.com/go-skynet/LocalAI

cd LocalAI

# copy your models to models/
cp your-model.bin models/

# (optional) Edit the .env file to set things like context size and threads
# vim .env

# start with docker-compose
docker compose up -d --build

# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"your-model.bin","object":"model"}]}

curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.bin",            
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

## Helm Chart Installation (run LocalAI in Kubernetes)
The local-ai Helm chart supports two options for the LocalAI server's models directory:
1. Basic deployment with no persistent volume. You must manually update the Deployment to configure your own models directory.

    Install the chart with `.Values.deployment.volumes.enabled == false` and `.Values.dataVolume.enabled == false`.

2. Advanced, two-phase deployment to provision the models directory using a DataVolume. Requires [Containerized Data Importer CDI](https://github.com/kubevirt/containerized-data-importer) to be pre-installed in your cluster.

    First, install the chart with `.Values.deployment.volumes.enabled == false` and `.Values.dataVolume.enabled == true`:
    ```bash
    helm install local-ai charts/local-ai -n local-ai --create-namespace
    ```
    Wait for CDI to create an importer Pod for the DataVolume and for the importer pod to finish provisioning the model archive inside the PV.

    Once the PV is provisioned and the importer Pod removed, set `.Values.deployment.volumes.enabled == true` and `.Values.dataVolume.enabled == false` and upgrade the chart:
    ```bash
    helm upgrade local-ai -n local-ai charts/local-ai
    ```
    This will update the local-ai Deployment to mount the PV that was provisioned by the DataVolume.

## Prompt templates 

The API doesn't inject a default prompt for talking to the model. You have to use a prompt similar to what's described in the standford-alpaca docs: https://github.com/tatsu-lab/stanford_alpaca#data-release.

<details>
You can use a default template for every model present in your model path, by creating a corresponding file with the `.tmpl` suffix next to your model. For instance, if the model is called `foo.bin`, you can create a sibiling file, `foo.bin.tmpl` which will be used as a default prompt, for instance this can be used with alpaca:

```
Below is an instruction that describes a task. Write a response that appropriately completes the request.

### Instruction:
{{.Input}}

### Response:
```

See the [prompt-templates](https://github.com/go-skynet/LocalAI/tree/master/prompt-templates) directory in this repository for templates for most popular models.

</details>

## API

`LocalAI` provides an API for running text generation as a service, that follows the OpenAI reference and can be used as a drop-in. The models once loaded the first time will be kept in memory.

<details>
Example of starting the API with `docker`:

```bash
docker run -p 8080:8080 -ti --rm quay.io/go-skynet/local-ai:latest --models-path /path/to/models --context-size 700 --threads 4
```

And you'll see:
```
┌───────────────────────────────────────────────────┐ 
│                   Fiber v2.42.0                   │ 
│               http://127.0.0.1:8080               │ 
│       (bound on host 0.0.0.0 and port 8080)       │ 
│                                                   │ 
│ Handlers ............. 1  Processes ........... 1 │ 
│ Prefork ....... Disabled  PID ................. 1 │ 
└───────────────────────────────────────────────────┘ 
```

You can control the API server options with command line arguments:

```
local-api --models-path <model_path> [--address <address>] [--threads <num_threads>]
```

The API takes takes the following parameters:

| Parameter    | Environment Variable | Default Value | Description                            |
| ------------ | -------------------- | ------------- | -------------------------------------- |
| models-path        | MODELS_PATH           |               | The path where you have models (ending with `.bin`).      |
| threads      | THREADS              | Number of Physical cores     | The number of threads to use for text generation. |
| address      | ADDRESS              | :8080         | The address and port to listen on. |
| context-size | CONTEXT_SIZE         | 512           | Default token context size. |

Once the server is running, you can start making requests to it using HTTP, using the OpenAI API. 

</details>

### Supported OpenAI API endpoints

You can check out the [OpenAI API reference](https://platform.openai.com/docs/api-reference/chat/create). 

Following the list of endpoints/parameters supported.

#### Chat completions

For example, to generate a chat completion, you can send a POST request to the `/v1/chat/completions` endpoint with the instruction as the request body:

```
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "ggml-koala-7b-model-q4_0-r2.bin",
     "messages": [{"role": "user", "content": "Say this is a test!"}],
     "temperature": 0.7
   }'
```

Available additional parameters: `top_p`, `top_k`, `max_tokens`

#### Completions

For example, to generate a comletion, you can send a POST request to the `/v1/completions` endpoint with the instruction as the request body:
```
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "ggml-koala-7b-model-q4_0-r2.bin",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

Available additional parameters: `top_p`, `top_k`, `max_tokens`

#### List models

You can list all the models available with:

```
curl http://localhost:8080/v1/models
```

## Using other models

gpt4all (https://github.com/nomic-ai/gpt4all) works as well, however the original model needs to be converted (same applies for old alpaca models, too):

```bash
wget -O tokenizer.model https://huggingface.co/decapoda-research/llama-30b-hf/resolve/main/tokenizer.model
mkdir models
cp gpt4all.. models/
git clone https://gist.github.com/eiz/828bddec6162a023114ce19146cb2b82
pip install sentencepiece
python 828bddec6162a023114ce19146cb2b82/gistfile1.txt models tokenizer.model
# There will be a new model with the ".tmp" extension, you have to use that one!
```

### Windows compatibility

It should work, however you need to make sure you give enough resources to the container. See https://github.com/go-skynet/LocalAI/issues/2

### Build locally

Pre-built images might fit well for most of the modern hardware, however you can and might need to build the images manually.

In order to build the `LocalAI` container image locally you can use `docker`:

```
# build the image
docker build -t LocalAI .
docker run LocalAI
```

Or build the binary with `make`:

```
make build
```

## Short-term roadmap

- [x] Mimic OpenAI API (https://github.com/go-skynet/LocalAI/issues/10)
- Binary releases (https://github.com/go-skynet/LocalAI/issues/6)
- Upstream our golang bindings to llama.cpp (https://github.com/ggerganov/llama.cpp/issues/351)
- [x] Multi-model support
- Have a webUI!

## License

MIT

## Acknowledgements

- [llama.cpp](https://github.com/ggerganov/llama.cpp)
- https://github.com/tatsu-lab/stanford_alpaca
- https://github.com/cornelk/llama-go for the initial ideas
- https://github.com/antimatter15/alpaca.cpp for the light model version (this is compatible and tested only with that checkpoint model!)
