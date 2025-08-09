
+++
disableToc = false
title = "Advanced usage"
weight = 21
url = '/advanced'
+++

### Advanced configuration with YAML files

In order to define default prompts, model parameters (such as custom default `top_p` or `top_k`), LocalAI can be configured to serve user-defined models with a set of default parameters and templates.

In order to configure a model, you can create multiple `yaml` files in the models path or either specify a single YAML configuration file. 
Consider the following `models` folder in the `example/chatbot-ui`:

```
base ❯ ls -liah examples/chatbot-ui/models 
36487587 drwxr-xr-x 2 mudler mudler 4.0K May  3 12:27 .
36487586 drwxr-xr-x 3 mudler mudler 4.0K May  3 10:42 ..
36465214 -rw-r--r-- 1 mudler mudler   10 Apr 27 07:46 completion.tmpl
36464855 -rw-r--r-- 1 mudler mudler   ?G Apr 27 00:08 luna-ai-llama2-uncensored.ggmlv3.q5_K_M.bin
36464537 -rw-r--r-- 1 mudler mudler  245 May  3 10:42 gpt-3.5-turbo.yaml
36467388 -rw-r--r-- 1 mudler mudler  180 Apr 27 07:46 chat.tmpl
```

In the `gpt-3.5-turbo.yaml` file it is defined the `gpt-3.5-turbo` model which is an alias to use `luna-ai-llama2` with pre-defined options.

For instance, consider the following that declares `gpt-3.5-turbo` backed by the `luna-ai-llama2` model:

```yaml
name: gpt-3.5-turbo
# Default model parameters
parameters:
  # Relative to the models path
  model: luna-ai-llama2-uncensored.ggmlv3.q5_K_M.bin
  # temperature
  temperature: 0.3
  # all the OpenAI request options here..

# Default context size
context_size: 512
threads: 10
# Define a backend (optional). By default it will try to guess the backend the first time the model is interacted with.
backend: llama-stable # available: llama, stablelm, gpt2, gptj rwkv

# Enable prompt caching
prompt_cache_path: "alpaca-cache"
prompt_cache_all: true

# stopwords (if supported by the backend)
stopwords:
- "HUMAN:"
- "### Response:"
# define chat roles
roles:
  assistant: '### Response:'
  system: '### System Instruction:'
  user: '### Instruction:'
template:
  # template file ".tmpl" with the prompt template to use by default on the endpoint call. Note there is no extension in the files
  completion: completion
  chat: chat
```

Specifying a `config-file` via CLI allows to declare models in a single file as a list, for instance:

```yaml
- name: list1
  parameters:
    model: testmodel
  context_size: 512
  threads: 10
  stopwords:
  - "HUMAN:"
  - "### Response:"
  roles:
    user: "HUMAN:"
    system: "GPT:"
  template:
    completion: completion
    chat: chat
- name: list2
  parameters:
    model: testmodel
  context_size: 512
  threads: 10
  stopwords:
  - "HUMAN:"
  - "### Response:"
  roles:
    user: "HUMAN:"
    system: "GPT:"
  template:
    completion: completion
   chat: chat
```

See also [chatbot-ui](https://github.com/mudler/LocalAI-examples/tree/main/chatbot-ui) as an example on how to use config files.

It is possible to specify a full URL or a short-hand URL to a YAML model configuration file and use it on start with local-ai, for example to use phi-2:

```
local-ai github://mudler/LocalAI/examples/configurations/phi-2.yaml@master
```

### Full config model file reference

```yaml
# Main configuration of the model, template, and system features.
name: "" # Model name, used to identify the model in API calls.

# Precision settings for the model, reducing precision can enhance performance on some hardware.
f16: null # Whether to use 16-bit floating-point precision.

embeddings: true # Enable embeddings for the model.

# Concurrency settings for the application.
threads: null # Number of threads to use for processing.

# Roles define how different entities interact in a conversational model.
# It can be used to map roles to specific parts of the conversation.
roles: {} # Roles for entities like user, system, assistant, etc.

# Backend to use for computation (like llama-cpp, diffusers, whisper).
backend: "" # Backend for AI computations.

# Templates for various types of model interactions.
template:
    chat: "" # Template for chat interactions. Uses golang templates with Sprig functions.
    chat_message: "" # Template for individual chat messages.  Uses golang templates with Sprig functions.
    completion: "" # Template for generating text completions. Uses golang templates with Sprig functions.
    edit: "" # Template for edit operations. Uses golang templates with Sprig functions.
    function: "" # Template for function calls. Uses golang templates with Sprig functions.
    use_tokenizer_template: false # Whether to use a specific tokenizer template. (vLLM)
    join_chat_messages_by_character: null # Character to join chat messages, if applicable. Defaults to newline.

# Function-related settings to control behavior of specific function calls.
function:
    disable_no_action: false # Whether to disable the no-action behavior.
    grammar:
        parallel_calls: false # Allow to return parallel tools
        disable_parallel_new_lines: false # Disable parallel processing for new lines in grammar checks.
        mixed_mode: false # Allow mixed-mode grammar enforcing
        no_mixed_free_string: false # Disallow free strings in mixed mode.
        disable: false # Completely disable grammar enforcing functionality.
        prefix: "" # Prefix to add before grammars rules.
        expect_strings_after_json: false # Expect string after JSON data.
    no_action_function_name: "" # Function name to call when no action is determined.
    no_action_description_name: "" # Description name for no-action functions.
    response_regex: [] # Regular expressions to match response from
    argument_regex: [] # Named regular to extract function arguments from the response.
    argument_regex_key_name: "key" # Name of the named regex capture to capture the key of the function arguments
	  argument_regex_value_name: "value" # Name of the named regex capture to capture the value of the function arguments
    json_regex_match: [] # Regular expressions to match JSON data when in tool mode
    replace_function_results: [] # Placeholder to replace function call results with arbitrary strings or patterns.
    replace_llm_results: [] # Replace language model results with arbitrary strings or patterns.
    capture_llm_results: [] # Capture language model results as text result, among JSON, in function calls. For instance, if a model returns a block for "thinking" and a block for "response", this will allow you to capture the thinking block.
    function_name_key: "name"
    function_arguments_key: "arguments"

# Feature gating flags to enable experimental or optional features.
feature_flags: {}

# System prompt to use by default.
system_prompt: ""

# Configuration for splitting tensors across GPUs.
tensor_split: ""

# Identifier for the main GPU used in multi-GPU setups.
main_gpu: ""

# Small value added to the denominator in RMS normalization to prevent division by zero.
rms_norm_eps: 0

# Natural question generation model parameter.
ngqa: 0

# Path where prompt cache is stored.
prompt_cache_path: ""

# Whether to cache all prompts.
prompt_cache_all: false

# Whether the prompt cache is read-only.
prompt_cache_ro: false

# Mirostat sampling settings.
mirostat_eta: null
mirostat_tau: null
mirostat: null

# GPU-specific layers configuration.
gpu_layers: null

# Memory mapping for efficient I/O operations.
mmap: null

# Memory locking to ensure data remains in RAM.
mmlock: null

# Mode to use minimal VRAM for GPU operations.
low_vram: null

# Words or phrases that halts processing.
stopwords: []

# Strings to cut from responses to maintain context or relevance.
cutstrings: []

# Strings to trim from responses for cleaner outputs.
trimspace: []
trimsuffix: []

# Default context size for the model's understanding of the conversation or text.
context_size: null

# Non-uniform memory access settings, useful for systems with multiple CPUs.
numa: false

# Configuration for LoRA
lora_adapter: ""
lora_base: ""
lora_scale: 0

# Disable matrix multiplication queuing in GPU operations.
no_mulmatq: false

# Model for generating draft responses.
draft_model: ""
n_draft: 0

# Quantization settings for the model, impacting memory and processing speed.
quantization: ""

# Utilization percentage of GPU memory to allocate for the model. (vLLM)
gpu_memory_utilization: 0

# Whether to trust and execute remote code.
trust_remote_code: false

# Force eager execution of TensorFlow operations if applicable. (vLLM)
enforce_eager: false

# Space allocated for swapping data in and out of memory. (vLLM)
swap_space: 0

# Maximum model length, possibly referring to the number of tokens or parameters. (vLLM)
max_model_len: 0

# Size of the tensor parallelism in distributed computing environments. (vLLM)
tensor_parallel_size: 0

# vision model to use for multimodal
mmproj: ""

# Disables offloading of key/value pairs in transformer models to save memory.
no_kv_offloading: false

# Scaling factor for the rope penalty.
rope_scaling: ""

# Type of configuration, often related to the type of task or model architecture.
type: ""

# YARN settings
yarn_ext_factor: 0
yarn_attn_factor: 0
yarn_beta_fast: 0
yarn_beta_slow: 0
# configuration for diffusers model
diffusers:
    cuda: false # Whether to use CUDA
    pipeline_type: "" # Type of pipeline to use.
    scheduler_type: "" # Type of scheduler for controlling operations.
    enable_parameters: "" # Parameters to enable in the diffuser.
    cfg_scale: 0 # Scale for CFG in the diffuser setup.
    img2img: false # Whether image-to-image transformation is supported.
    clip_skip: 0 # Number of steps to skip in CLIP operations.
    clip_model: "" # Model to use for CLIP operations.
    clip_subfolder: "" # Subfolder for storing CLIP-related data.
    control_net: "" # Control net to use

# Step count, usually for image processing models
step: 0

# Configuration for gRPC communication.
grpc:
    attempts: 0 # Number of retry attempts for gRPC calls.
    attempts_sleep_time: 0 # Sleep time between retries.

# Text-to-Speech (TTS) configuration.
tts:
    voice: "" # Voice setting for TTS.
    vall-e:
        audio_path: "" # Path to audio files for Vall-E.

# Whether to use CUDA for GPU-based operations.
cuda: false

# List of files to download as part of the setup or operations.
download_files: []
```

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
# ...
```

### Automatic prompt caching

LocalAI can automatically cache prompts for faster loading of the prompt. This can be useful if your model need a prompt template with prefixed text in the prompt before the input.

To enable prompt caching, you can control the settings in the model config YAML file:

```yaml

# Enable prompt caching
prompt_cache_path: "cache"
prompt_cache_all: true

```

`prompt_cache_path` is relative to the models folder. you can enter here a name for the file that will be automatically create during the first load if `prompt_cache_all` is set to `true`.

### Configuring a specific backend for the model

By default LocalAI will try to autoload the model by trying all the backends. This might work for most of models, but some of the backends are NOT configured to autoload.

The available backends are listed in the [model compatibility table]({{%relref "docs/reference/compatibility-table" %}}).

In order to specify a backend for your models, create a model config file in your `models` directory specifying the backend:

```yaml
name: gpt-3.5-turbo

# Default model parameters
parameters:
  # Relative to the models path
  model: ...

backend: llama-stable
# ...
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

{{< table "table-responsive" >}}
| Environment variable       | Default | Description                                                                                                |
|----------------------------|---------|------------------------------------------------------------------------------------------------------------|
| `REBUILD`                  | `false` | Rebuild LocalAI on startup                                                                                 |
| `BUILD_TYPE`               |         | Build type. Available: `cublas`, `openblas`, `clblas`, `intel` (intel core), `sycl_f16`, `sycl_f32` (intel backends)                                                      |
| `GO_TAGS`                  |         | Go tags. Available: `stablediffusion`                                                                      |
| `HUGGINGFACEHUB_API_TOKEN` |         | Special token for interacting with HuggingFace Inference API, required only when using the `langchain-huggingface` backend |
| `EXTRA_BACKENDS`          |         | A space separated list of backends to prepare. For example `EXTRA_BACKENDS="backend/python/diffusers backend/python/transformers"` prepares the python environment on start |
| `DISABLE_AUTODETECT`       | `false` | Disable autodetect of CPU flagset on start                                                                     |
| `LLAMACPP_GRPC_SERVERS`   |         | A list of llama.cpp workers to distribute the workload. For example `LLAMACPP_GRPC_SERVERS="address1:port,address2:port"` |
{{< /table >}}

Here is how to configure these variables:

```bash
# Option 1: command line
docker run --env REBUILD=true localai
# Option 2: set within an env file
docker run --env-file .env localai
```

### CLI parameters

You can control LocalAI with command line arguments, to specify a binding address, or the number of threads. Any command line parameter can be specified via an environment variable.

In the help text below, BASEPATH is the location that local-ai is being executed from

#### Global Flags
{{< table "table-responsive" >}}
| Parameter | Default | Description | Environment Variable |
|-----------|---------|-------------|----------------------|
|  -h, --help |  | Show context-sensitive help. |
| --log-level | info | Set the level of logs to output [error,warn,info,debug] | $LOCALAI_LOG_LEVEL |
{{< /table >}}

#### Storage Flags
{{< table "table-responsive" >}}
| Parameter | Default | Description | Environment Variable |
|-----------|---------|-------------|----------------------|
| --models-path | BASEPATH/models | Path containing models used for inferencing  | $LOCALAI_MODELS_PATH |
| --backend-assets-path |/tmp/localai/backend_data | Path used to extract libraries that are required by some of the backends in runtime | $LOCALAI_BACKEND_ASSETS_PATH |
| --generated-content-path | /tmp/generated/content | Location for assets generated by backends (e.g. stablediffusion) | $LOCALAI_GENERATED_CONTENT_PATH |
| --upload-path | /tmp/localai/upload | Path to store uploads from files api | $LOCALAI_UPLOAD_PATH |
| --config-path | /tmp/localai/config | | $LOCALAI_CONFIG_PATH |
| --localai-config-dir | BASEPATH/configuration | Directory for dynamic loading of certain configuration files (currently api_keys.json and external_backends.json) | $LOCALAI_CONFIG_DIR |
| --localai-config-dir-poll-interval |  | Typically the config path picks up changes automatically, but if your system has broken fsnotify events, set this to a time duration to poll the LocalAI Config Dir (example: 1m) | $LOCALAI_CONFIG_DIR_POLL_INTERVAL |
| --models-config-file | STRING | YAML file containing a list of model backend configs | $LOCALAI_MODELS_CONFIG_FILE |
{{< /table >}}

#### Models Flags
{{< table "table-responsive" >}}
| Parameter | Default | Description | Environment Variable |
|-----------|---------|-------------|----------------------|
| --galleries | STRING | JSON list of galleries | $LOCALAI_GALLERIES |
| --autoload-galleries |  | | $LOCALAI_AUTOLOAD_GALLERIES |
| --remote-library | "https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/model_library.yaml" | A LocalAI remote library URL | $LOCALAI_REMOTE_LIBRARY |
| --preload-models | STRING | A List of models to apply in JSON at start |$LOCALAI_PRELOAD_MODELS |
| --models | MODELS,... | A List of model configuration URLs to load | $LOCALAI_MODELS |
| --preload-models-config | STRING | A List of models to apply at startup. Path to a YAML config file | $LOCALAI_PRELOAD_MODELS_CONFIG |
{{< /table >}}

#### Performance Flags
{{< table "table-responsive" >}}
| Parameter | Default | Description | Environment Variable |
|-----------|---------|-------------|----------------------|
| --f16 |  | Enable GPU acceleration | $LOCALAI_F16 |
| -t, --threads | 4 | Number of threads used for parallel computation. Usage of the number of physical cores in the system is suggested | $LOCALAI_THREADS |
| --context-size | 512 | Default context size for models | $LOCALAI_CONTEXT_SIZE |
{{< /table >}}

#### API Flags
{{< table "table-responsive" >}}
| Parameter | Default | Description | Environment Variable |
|-----------|---------|-------------|----------------------|
| --address | ":8080" | Bind address for the API server | $LOCALAI_ADDRESS |
| --cors |  |  | $LOCALAI_CORS |
| --cors-allow-origins |  |  | $LOCALAI_CORS_ALLOW_ORIGINS |
| --upload-limit | 15 | Default upload-limit in MB | $LOCALAI_UPLOAD_LIMIT |
| --api-keys | API-KEYS,... | List of API Keys to enable API authentication. When this is set, all the requests must be authenticated with one of these API keys | $LOCALAI_API_KEY |
| --disable-welcome |  | Disable welcome pages | $LOCALAI_DISABLE_WELCOME |
| --disable-webui | false | Disables the web user interface. When set to true, the server will only expose API endpoints without serving the web interface | $LOCALAI_DISABLE_WEBUI |
| --machine-tag |  | If not empty - put that string to Machine-Tag header in each response. Useful to track response from different machines using multiple P2P federated nodes | $LOCALAI_MACHINE_TAG |
{{< /table >}}

#### Backend Flags
{{< table "table-responsive" >}}
| Parameter | Default | Description | Environment Variable |
|-----------|---------|-------------|----------------------|
| --parallel-requests |  | Enable backends to handle multiple requests in parallel if they support it (e.g.: llama.cpp or vllm) | $LOCALAI_PARALLEL_REQUESTS |
| --single-active-backend |  | Allow only one backend to be run at a time | $LOCALAI_SINGLE_ACTIVE_BACKEND |
| --preload-backend-only |  | Do not launch the API services, only the preloaded models / backends are started (useful for multi-node setups) | $LOCALAI_PRELOAD_BACKEND_ONLY |
| --external-grpc-backends | EXTERNAL-GRPC-BACKENDS,... | A list of external grpc backends | $LOCALAI_EXTERNAL_GRPC_BACKENDS |
| --enable-watchdog-idle |  | Enable watchdog for stopping backends that are idle longer than the watchdog-idle-timeout | $LOCALAI_WATCHDOG_IDLE |
| --watchdog-idle-timeout | 15m | Threshold beyond which an idle backend should be stopped | $LOCALAI_WATCHDOG_IDLE_TIMEOUT, $WATCHDOG_IDLE_TIMEOUT |
| --enable-watchdog-busy |  | Enable watchdog for stopping backends that are busy longer than the watchdog-busy-timeout | $LOCALAI_WATCHDOG_BUSY |
| --watchdog-busy-timeout | 5m | Threshold beyond which a busy backend should be stopped | $LOCALAI_WATCHDOG_BUSY_TIMEOUT |
{{< /table >}}

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
# PYTHON_GRPC_MAX_WORKERS=1

### Define the number of parallel LLAMA.cpp workers (Defaults to 1)
# LLAMACPP_PARALLEL=1

### Enable to run parallel requests
# LOCALAI_PARALLEL_REQUESTS=true
```

Note that, for llama.cpp you need to set accordingly `LLAMACPP_PARALLEL` to the number of parallel processes your GPU/CPU can handle. For python-based backends (like vLLM) you can set `PYTHON_GRPC_MAX_WORKERS` to the number of parallel requests.

### Disable CPU flagset auto detection in llama.cpp

LocalAI will automatically discover the CPU flagset available in your host and will use the most optimized version of the backends.

If you want to disable this behavior, you can set `DISABLE_AUTODETECT` to `true` in the environment variables.
