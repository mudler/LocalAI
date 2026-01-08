
+++
disableToc = false
title = "Advanced usage"
weight = 21
url = '/advanced'
+++

### Model Configuration with YAML Files

LocalAI uses YAML configuration files to define model parameters, templates, and behavior. You can create individual YAML files in the models directory or use a single configuration file with multiple models.

**Quick Example:**

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

For a complete reference of all available configuration options, see the [Model Configuration]({{%relref "advanced/model-configuration" %}}) page.

**Configuration File Locations:**

1. **Individual files**: Create `.yaml` files in your models directory (e.g., `models/gpt-3.5-turbo.yaml`)
2. **Single config file**: Use `--models-config-file` or `LOCALAI_MODELS_CONFIG_FILE` to specify a file containing multiple models
3. **Remote URLs**: Specify a URL to a YAML configuration file at startup:
   ```bash
   local-ai run github://mudler/LocalAI/examples/configurations/phi-2.yaml@master
   ```

See also [chatbot-ui](https://github.com/mudler/LocalAI-examples/tree/main/chatbot-ui) as an example on how to use config files.

### Prompt templates 

The API doesn't inject a default prompt for talking to the model. You have to use a prompt similar to what's described in the standford-alpaca docs: https://github.com/tatsu-lab/stanford_alpaca#data-release.

<details>
You can use a default template for every model present in your model path, by creating a corresponding file with the `.tmpl` suffix next to your model. For instance, if the model is called `foo.bin`, you can create a sibling file, `foo.bin.tmpl` which will be used as a default prompt and can be used with alpaca:

```
The below instruction describes a task. Write a response that appropriately completes the request.

### Instruction:
{{.Input}}

### Response:
```

See the [prompt-templates](https://github.com/go-skynet/LocalAI/tree/master/prompt-templates) directory in this repository for templates for some of the most popular models.


For the edit endpoint, an example template for alpaca-based models can be:

```yaml
Below is an instruction that describes a task, paired with an input that provides further context. Write a response that appropriately completes the request.

### Instruction:
{{.Instruction}}

### Input:
{{.Input}}

### Response:
```

</details>

### Install models using the API

Instead of installing models manually, you can use the LocalAI API endpoints and a model definition to install programmatically via API models in runtime.

A curated collection of model files is in the [model-gallery](https://github.com/mudler/LocalAI/tree/master/gallery). The files of the model gallery are different from the model files used to configure LocalAI models. The model gallery files contains information about the model setup, and the files necessary to run the model locally.

To install for example `lunademo`, you can send a POST call to the `/models/apply` endpoint with the model definition url (`url`) and the name of the model should have in LocalAI (`name`, optional):

```bash
curl --location 'http://localhost:8080/models/apply' \
--header 'Content-Type: application/json' \
--data-raw '{
    "id": "TheBloke/Luna-AI-Llama2-Uncensored-GGML/luna-ai-llama2-uncensored.ggmlv3.q5_K_M.bin",
    "name": "lunademo"
}'
```


### Preloading models during startup

In order to allow the API to start-up with all the needed model on the first-start, the model gallery files can be used during startup. 

```bash
PRELOAD_MODELS='[{"url": "https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml","name": "gpt4all-j"}]' local-ai
```

`PRELOAD_MODELS` (or `--preload-models`) takes a list in JSON with the same parameter of the API calls of the `/models/apply` endpoint.

Similarly it can be specified a path to a YAML configuration file containing a list of models with `PRELOAD_MODELS_CONFIG` ( or `--preload-models-config` ):

```yaml
- url: https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml
  name: gpt4all-j
```

### Automatic prompt caching

LocalAI can automatically cache prompts for faster loading of the prompt. This can be useful if your model need a prompt template with prefixed text in the prompt before the input.

To enable prompt caching, you can control the settings in the model config YAML file:

```yaml

prompt_cache_path: "cache"
prompt_cache_all: true

```

`prompt_cache_path` is relative to the models folder. you can enter here a name for the file that will be automatically create during the first load if `prompt_cache_all` is set to `true`.

### Configuring a specific backend for the model

By default LocalAI will try to autoload the model by trying all the backends. This might work for most of models, but some of the backends are NOT configured to autoload.

The available backends are listed in the [model compatibility table]({{%relref "reference/compatibility-table" %}}).

In order to specify a backend for your models, create a model config file in your `models` directory specifying the backend:

```yaml
name: gpt-3.5-turbo

parameters:
  # Relative to the models path
  model: ...

backend: llama-stable
```

### Connect external backends

LocalAI backends are internally implemented using `gRPC` services. This also allows `LocalAI` to connect to external `gRPC` services on start and extend LocalAI functionalities via third-party binaries.

The `--external-grpc-backends` parameter in the CLI can be used either to specify a local backend (a file) or a remote URL. The syntax is `<BACKEND_NAME>:<BACKEND_URI>`. Once LocalAI is started with it, the new backend name will be available for all the API endpoints.

So for instance, to register a new backend which is a local file:

```
./local-ai --debug --external-grpc-backends "my-awesome-backend:/path/to/my/backend.py"
```

Or a remote URI:

```
./local-ai --debug --external-grpc-backends "my-awesome-backend:host:port"
```

For example, to start vllm manually after compiling LocalAI (also assuming running the command from the root of the repository):

```bash
./local-ai --external-grpc-backends "vllm:$PWD/backend/python/vllm/run.sh"
```

Note that first is is necessary to create the environment with:

```bash
make -C backend/python/vllm
```


### Environment variables

When LocalAI runs in a container,
there are additional environment variables available that modify the behavior of LocalAI on startup:

| Environment variable       | Default | Description                                                                                                |
|----------------------------|---------|------------------------------------------------------------------------------------------------------------|
| `REBUILD`                  | `false` | Rebuild LocalAI on startup                                                                                 |
| `BUILD_TYPE`               |         | Build type. Available: `cublas`, `openblas`, `clblas`, `intel` (intel core), `sycl_f16`, `sycl_f32` (intel backends)                                                      |
| `GO_TAGS`                  |         | Go tags. Available: `stablediffusion`                                                                      |
| `HUGGINGFACEHUB_API_TOKEN` |         | Special token for interacting with HuggingFace Inference API, required only when using the `langchain-huggingface` backend |
| `EXTRA_BACKENDS`          |         | A space separated list of backends to prepare. For example `EXTRA_BACKENDS="backend/python/diffusers backend/python/transformers"` prepares the python environment on start |
| `DISABLE_AUTODETECT`       | `false` | Disable autodetect of CPU flagset on start                                                                     |
| `LLAMACPP_GRPC_SERVERS`   |         | A list of llama.cpp workers to distribute the workload. For example `LLAMACPP_GRPC_SERVERS="address1:port,address2:port"` |

Here is how to configure these variables:

```bash
docker run --env REBUILD=true localai
docker run --env-file .env localai
```

### CLI Parameters

For a complete reference of all CLI parameters, environment variables, and command-line options, see the [CLI Reference]({{%relref "reference/cli-reference" %}}) page.

You can control LocalAI with command line arguments to specify a binding address, number of threads, model paths, and many other options. Any command line parameter can be specified via an environment variable.

### .env files

Any settings being provided by an Environment Variable can also be provided from within .env files.  There are several locations that will be checked for relevant .env files. In order of precedence they are:

- .env within the current directory
- localai.env within the current directory
- localai.env within the home directory
- .config/localai.env within the home directory
- /etc/localai.env

Environment variables within files earlier in the list will take precedence over environment variables defined in files later in the list.

An example .env file is:

```
LOCALAI_THREADS=10
LOCALAI_MODELS_PATH=/mnt/storage/localai/models
LOCALAI_F16=true
```

### Request headers

You can use 'Extra-Usage' request header key presence ('Extra-Usage: true') to receive inference timings in milliseconds extending default OpenAI response model in the usage field:   
```
...
{
  "id": "...",
  "created": ...,
  "model": "...",
  "choices": [
    {
      ...
    },
    ...
  ],
  "object": "...",
  "usage": {
    "prompt_tokens": ...,
    "completion_tokens": ...,
    "total_tokens": ...,
    // Extra-Usage header key will include these two float fields:
    "timing_prompt_processing: ...,
    "timing_token_generation": ...,
  },
}
...
```

### Extra backends

LocalAI can be extended with extra backends. The backends are implemented as `gRPC` services and can be written in any language. See the [backend section](https://localai.io/backends/) for more details on how to install and build new backends for LocalAI.

#### In runtime

When using the `-core` container image it is possible to prepare the python backends you are interested into by using the `EXTRA_BACKENDS` variable, for instance:

```bash
docker run --env EXTRA_BACKENDS="backend/python/diffusers" quay.io/go-skynet/local-ai:master
```

### Concurrent requests

LocalAI supports parallel requests for the backends that supports it. For instance, vLLM and llama.cpp supports parallel requests, and thus LocalAI allows to run multiple requests in parallel. 

In order to enable parallel requests, you have to pass `--parallel-requests` or set the `PARALLEL_REQUEST` to true as environment variable.

A list of the environment variable that tweaks parallelism is the following:

```
### Python backends GRPC max workers
### Default number of workers for GRPC Python backends.
### This actually controls wether a backend can process multiple requests or not.

### Define the number of parallel LLAMA.cpp workers (Defaults to 1)

### Enable to run parallel requests
```

Note that, for llama.cpp you need to set accordingly `LLAMACPP_PARALLEL` to the number of parallel processes your GPU/CPU can handle. For python-based backends (like vLLM) you can set `PYTHON_GRPC_MAX_WORKERS` to the number of parallel requests.

### VRAM and Memory Management

For detailed information on managing VRAM when running multiple models, see the dedicated [VRAM and Memory Management]({{%relref "advanced/vram-management" %}}) page.

### Disable CPU flagset auto detection in llama.cpp

LocalAI will automatically discover the CPU flagset available in your host and will use the most optimized version of the backends.

If you want to disable this behavior, you can set `DISABLE_AUTODETECT` to `true` in the environment variables.
