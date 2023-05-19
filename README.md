<h1 align="center">
  <br>
  <img height="300" src="https://user-images.githubusercontent.com/2420543/233147843-88697415-6dbf-4368-a862-ab217f9f7342.jpeg"> <br>
    LocalAI
<br>
</h1>

[![tests](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml) [![build container images](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml)

[![](https://dcbadge.vercel.app/api/server/uJAeKSAGDy?style=flat-square&theme=default-inverted)](https://discord.gg/uJAeKSAGDy) 

**LocalAI** is a drop-in replacement REST API that's compatible with OpenAI API specifications for local inferencing. It allows you to run models locally or on-prem with consumer grade hardware, supporting multiple model families that are compatible with the ggml format.

For a list of the supported model families, please see [the model compatibility table below](https://github.com/go-skynet/LocalAI#model-compatibility-table).

In a nutshell:

- Local, OpenAI drop-in alternative REST API. You own your data.
- NO GPU required. NO Internet access is required either. Optional, GPU Acceleration is available in `llama.cpp`-compatible LLMs. [See building instructions](https://github.com/go-skynet/LocalAI#cublas).
- Supports multiple models, Audio transcription, Text generation with GPTs, Image generation with stable diffusion (experimental)
- Once loaded the first time, it keep models loaded in memory for faster inference
- Doesn't shell-out, but uses C++ bindings for a faster inference and better performance. 

LocalAI is a community-driven project, focused on making the AI accessible to anyone. Any contribution, feedback and PR is welcome! It was initially created by [mudler](https://github.com/mudler/) at the [SpectroCloud OSS Office](https://github.com/spectrocloud).

See the [usage](https://github.com/go-skynet/LocalAI#usage) and [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/) sections to learn how to use LocalAI.

### How does it work?  

<details>

LocalAI is an API written in Go that serves as an OpenAI shim, enabling software already developed with OpenAI SDKs to seamlessly integrate with LocalAI. It can be effortlessly implemented as a substitute, even on consumer-grade hardware. This capability is achieved by employing various C++ backends, including [ggml](https://github.com/ggerganov/ggml), to perform inference on LLMs using both CPU and, if desired, GPU.

LocalAI uses C++ bindings for optimizing speed. It is based on [llama.cpp](https://github.com/ggerganov/llama.cpp), [gpt4all](https://github.com/nomic-ai/gpt4all), [rwkv.cpp](https://github.com/saharNooby/rwkv.cpp), [ggml](https://github.com/ggerganov/ggml), [whisper.cpp](https://github.com/ggerganov/whisper.cpp) for audio transcriptions, [bert.cpp](https://github.com/skeskinen/bert.cpp) for embedding and [StableDiffusion-NCN](https://github.com/EdVince/Stable-Diffusion-NCNN) for image generation. See [the model compatibility table](https://github.com/go-skynet/LocalAI#model-compatibility-table) to learn about all the components of LocalAI.

![LocalAI](https://github.com/go-skynet/LocalAI/assets/2420543/38de3a9b-3866-48cd-9234-662f9571064a)

</details>

## News

- 19-05-2023: __v1.13.0__ released! 🔥🔥 updates to the `gpt4all` and `llama` backend, consolidated CUDA support ( https://github.com/go-skynet/LocalAI/pull/310 thanks to @bubthegreat and @Thireus ), preliminar support for [installing models via API](https://github.com/go-skynet/LocalAI#advanced-prepare-models-using-the-api).
- 17-05-2023:  __v1.12.0__ released! 🔥🔥 Minor fixes, plus CUDA (https://github.com/go-skynet/LocalAI/pull/258) support for `llama.cpp`-compatible models and image generation (https://github.com/go-skynet/LocalAI/pull/272).
- 16-05-2023: 🔥🔥🔥 Experimental support for CUDA (https://github.com/go-skynet/LocalAI/pull/258) in the `llama.cpp` backend and Stable diffusion CPU image generation (https://github.com/go-skynet/LocalAI/pull/272) in `master`.

Now LocalAI can generate images too:

| mode=0                                                                                                                | mode=1 (winograd/sgemm)                                                                                                                |
|------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| ![b6441997879](https://github.com/go-skynet/LocalAI/assets/2420543/d50af51c-51b7-4f39-b6c2-bf04c403894c)              | ![winograd2](https://github.com/go-skynet/LocalAI/assets/2420543/1935a69a-ecce-4afc-a099-1ac28cb649b3)                |

- 14-05-2023: __v1.11.1__ released! `rwkv` backend patch release
- 13-05-2023: __v1.11.0__ released! 🔥 Updated `llama.cpp` bindings: This update includes a breaking change in the model files ( https://github.com/ggerganov/llama.cpp/pull/1405 ) - old models should still work with the `gpt4all-llama` backend.
- 12-05-2023: __v1.10.0__ released! 🔥🔥 Updated `gpt4all` bindings. Added support for GPTNeox (experimental), RedPajama (experimental), Starcoder (experimental), Replit (experimental), MosaicML MPT. Also now `embeddings` endpoint supports tokens arrays. See the [langchain-chroma](https://github.com/go-skynet/LocalAI/tree/master/examples/langchain-chroma) example! Note - this update does NOT include https://github.com/ggerganov/llama.cpp/pull/1405 which makes models incompatible.
- 11-05-2023: __v1.9.0__ released! 🔥 Important whisper updates ( https://github.com/go-skynet/LocalAI/pull/233 https://github.com/go-skynet/LocalAI/pull/229 ) and extended gpt4all model families support ( https://github.com/go-skynet/LocalAI/pull/232 ). Redpajama/dolly experimental ( https://github.com/go-skynet/LocalAI/pull/214 )
- 10-05-2023: __v1.8.0__ released! 🔥 Added support for fast and accurate embeddings with `bert.cpp` ( https://github.com/go-skynet/LocalAI/pull/222 )
- 09-05-2023: Added experimental support for transcriptions endpoint ( https://github.com/go-skynet/LocalAI/pull/211 )
- 08-05-2023: Support for embeddings with models using the `llama.cpp` backend ( https://github.com/go-skynet/LocalAI/pull/207 )
- 02-05-2023: Support for `rwkv.cpp` models ( https://github.com/go-skynet/LocalAI/pull/158 ) and for `/edits` endpoint
- 01-05-2023: Support for SSE stream of tokens in `llama.cpp` backends ( https://github.com/go-skynet/LocalAI/pull/152 )

Twitter: [@LocalAI_API](https://twitter.com/LocalAI_API) and [@mudler_it](https://twitter.com/mudler_it)

### Blogs and articles

- [Question Answering on Documents locally with LangChain, LocalAI, Chroma, and GPT4All](https://mudler.pm/posts/localai-question-answering/) by Ettore Di Giacinto
- [Tutorial to use k8sgpt with LocalAI](https://medium.com/@tyler_97636/k8sgpt-localai-unlock-kubernetes-superpowers-for-free-584790de9b65) - excellent usecase for localAI, using AI to analyse Kubernetes clusters. by Tyller Gillson

## Contribute and help

To help the project you can:

- Upvote the [Reddit post](https://www.reddit.com/r/selfhosted/comments/12w4p2f/localai_openai_compatible_api_to_run_llm_models/) about LocalAI.

- [Hacker news post](https://news.ycombinator.com/item?id=35726934) - help us out by voting if you like this project.

- If you have technological skills and want to contribute to development, have a look at the open issues. If you are new you can have a look at the [good-first-issue](https://github.com/go-skynet/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22) and [help-wanted](https://github.com/go-skynet/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3A%22help+wanted%22) labels.

- If you don't have technological skills you can still help improving documentation or add examples or share your user-stories with our community, any help and contribution is welcome!

## Model compatibility

It is compatible with the models supported by [llama.cpp](https://github.com/ggerganov/llama.cpp) supports also [GPT4ALL-J](https://github.com/nomic-ai/gpt4all) and [cerebras-GPT with ggml](https://huggingface.co/lxe/Cerebras-GPT-2.7B-Alpaca-SP-ggml).

Tested with:
- Vicuna
- Alpaca
- [GPT4ALL](https://gpt4all.io)
- [GPT4ALL-J](https://gpt4all.io/models/ggml-gpt4all-j.bin) (no changes required)
- Koala
- [cerebras-GPT with ggml](https://huggingface.co/lxe/Cerebras-GPT-2.7B-Alpaca-SP-ggml)
- WizardLM
- [RWKV](https://github.com/BlinkDL/RWKV-LM) models with [rwkv.cpp](https://github.com/saharNooby/rwkv.cpp)

Note: You might need to convert some models from older models to the new format, for indications, see [the README in llama.cpp](https://github.com/ggerganov/llama.cpp#using-gpt4all) for instance to run `gpt4all`.

### RWKV

<details>

A full example on how to run a rwkv model is in the [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/rwkv).

Note: rwkv models needs to specify the backend `rwkv` in the YAML config files and have an associated tokenizer along that needs to be provided with it:

```
36464540 -rw-r--r--  1 mudler mudler 1.2G May  3 10:51 rwkv_small
36464543 -rw-r--r--  1 mudler mudler 2.4M May  3 10:51 rwkv_small.tokenizer.json
```

</details>

### Others

It should also be compatible with StableLM and GPTNeoX ggml models (untested).

### Hardware requirements

Depending on the model you are attempting to run might need more RAM or CPU resources. Check out also [here](https://github.com/ggerganov/llama.cpp#memorydisk-requirements) for `ggml` based backends. `rwkv` is less expensive on resources.


### Model compatibility table

<details>

| Backend and Bindings                                                             | Compatible models     | Completion/Chat endpoint | Audio transcription/Image | Embeddings support                | Token stream support |
|----------------------------------------------------------------------------------|-----------------------|--------------------------|---------------------------|-----------------------------------|----------------------|
| [llama](https://github.com/ggerganov/llama.cpp) ([binding](https://github.com/go-skynet/go-llama.cpp))         | Vicuna, Alpaca, LLaMa | yes                      | no                        | yes (doesn't seem to be accurate) | yes                  |
| [gpt4all-llama](https://github.com/nomic-ai/gpt4all)      | Vicuna, Alpaca, LLaMa | yes                      | no                        | no                                | yes                  |
| [gpt4all-mpt](https://github.com/nomic-ai/gpt4all)          | MPT                   | yes                      | no                        | no                                | yes                  |
| [gpt4all-j](https://github.com/nomic-ai/gpt4all)           | GPT4ALL-J             | yes                      | no                        | no                                | yes                  |
| [gpt2](https://github.com/ggerganov/ggml) ([binding](https://github.com/go-skynet/go-gpt2.cpp))             | GPT/NeoX, Cerebras    | yes                      | no                        | no                                | no                   |
| [dolly](https://github.com/ggerganov/ggml) ([binding](https://github.com/go-skynet/go-gpt2.cpp))            | Dolly                 | yes                      | no                        | no                                | no                   |
| [redpajama](https://github.com/ggerganov/ggml) ([binding](https://github.com/go-skynet/go-gpt2.cpp))        | RedPajama             | yes                      | no                        | no                                | no                   |
| [stableLM](https://github.com/ggerganov/ggml) ([binding](https://github.com/go-skynet/go-gpt2.cpp))         | StableLM GPT/NeoX     | yes                      | no                        | no                                | no                   |
| [replit](https://github.com/ggerganov/ggml) ([binding](https://github.com/go-skynet/go-gpt2.cpp))        | Replit             | yes                      | no                        | no                                | no                   |
| [gptneox](https://github.com/ggerganov/ggml) ([binding](https://github.com/go-skynet/go-gpt2.cpp))        | GPT NeoX             | yes                      | no                        | no                                | no                   |
| [starcoder](https://github.com/ggerganov/ggml) ([binding](https://github.com/go-skynet/go-gpt2.cpp))        | Starcoder             | yes                      | no                        | no                                | no                   |
| [bloomz](https://github.com/NouamaneTazi/bloomz.cpp) ([binding](https://github.com/go-skynet/bloomz.cpp))       | Bloom                 | yes                      | no                        | no                                | no                   |
| [rwkv](https://github.com/saharNooby/rwkv.cpp) ([binding](https://github.com/donomii/go-rw))       | rwkv                 | yes                      | no                        | no                                | yes                   |
| [bert](https://github.com/skeskinen/bert.cpp) ([binding](https://github.com/go-skynet/go-bert.cpp) | bert                  | no                       | no                  | yes                               | no                   |    
| [whisper](https://github.com/ggerganov/whisper.cpp)         | whisper               | no                       | Audio                 | no                                | no                   |  
| [stablediffusion](https://github.com/EdVince/Stable-Diffusion-NCNN) ([binding](https://github.com/mudler/go-stable-diffusion))        | stablediffusion               | no                       | Image                 | no                                | no                   | 
</details>

## Usage

> `LocalAI` comes by default as a container image. You can check out all the available images with corresponding tags [here](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest).

The easiest way to run LocalAI is by using `docker-compose` (to build locally, see [building LocalAI](https://github.com/go-skynet/LocalAI/tree/master#setup)):

```bash

git clone https://github.com/go-skynet/LocalAI

cd LocalAI

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# copy your models to models/
cp your-model.bin models/

# (optional) Edit the .env file to set things like context size and threads
# vim .env

# start with docker-compose
docker-compose up -d --pull always
# or you can build the images with:
# docker-compose up -d --build

# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"your-model.bin","object":"model"}]}

curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.bin",            
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

### Example: Use GPT4ALL-J model

<details>

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# Download gpt4all-j to models/
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# Use a template from the examples
cp -rf prompt-templates/ggml-gpt4all-j.tmpl models/

# (optional) Edit the .env file to set things like context size and threads
# vim .env

# start with docker-compose
docker-compose up -d --pull always
# or you can build the images with:
# docker-compose up -d --build
# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"ggml-gpt4all-j","object":"model"}]}

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "ggml-gpt4all-j",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9 
   }'

# {"model":"ggml-gpt4all-j","choices":[{"message":{"role":"assistant","content":"I'm doing well, thanks. How about you?"}}]}
```
</details>

### Advanced: prepare models using the API

Instead of installing models manually, you can use the LocalAI API endpoints and a model definition to install programmatically via API models in runtime.

<details>

A curated collection of model files is in the [model-gallery](https://github.com/go-skynet/model-gallery) (work in progress!).

To install for example `gpt4all-j`, you can send a POST call to the `/models/apply` endpoint with the model definition url (`url`) and the name of the model should have in LocalAI (`name`, optional):

```
curl http://localhost:8080/models/apply -H "Content-Type: application/json" -d '{
     "url": "https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml",
     "name": "gpt4all-j"
   }'  
```

</details>


### Other examples

![Screenshot from 2023-04-26 23-59-55](https://user-images.githubusercontent.com/2420543/234715439-98d12e03-d3ce-4f94-ab54-2b256808e05e.png)

To see other examples on how to integrate with other projects for instance for question answering or for using it with chatbot-ui, see: [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/).


### Advanced configuration

LocalAI can be configured to serve user-defined models with a set of default parameters and templates.

<details>

You can create multiple `yaml` files in the models path or either specify a single YAML configuration file. 
Consider the following `models` folder in the `example/chatbot-ui`:

```
base ❯ ls -liah examples/chatbot-ui/models 
36487587 drwxr-xr-x 2 mudler mudler 4.0K May  3 12:27 .
36487586 drwxr-xr-x 3 mudler mudler 4.0K May  3 10:42 ..
36465214 -rw-r--r-- 1 mudler mudler   10 Apr 27 07:46 completion.tmpl
36464855 -rw-r--r-- 1 mudler mudler 3.6G Apr 27 00:08 ggml-gpt4all-j
36464537 -rw-r--r-- 1 mudler mudler  245 May  3 10:42 gpt-3.5-turbo.yaml
36467388 -rw-r--r-- 1 mudler mudler  180 Apr 27 07:46 gpt4all.tmpl
```

In the `gpt-3.5-turbo.yaml` file it is defined the `gpt-3.5-turbo` model which is an alias to use `gpt4all-j` with pre-defined options.

For instance, consider the following that declares `gpt-3.5-turbo` backed by the `ggml-gpt4all-j` model:

```yaml
name: gpt-3.5-turbo
# Default model parameters
parameters:
  # Relative to the models path
  model: ggml-gpt4all-j
  # temperature
  temperature: 0.3
  # all the OpenAI request options here..

# Default context size
context_size: 512
threads: 10
# Define a backend (optional). By default it will try to guess the backend the first time the model is interacted with.
backend: gptj # available: llama, stablelm, gpt2, gptj rwkv
# stopwords (if supported by the backend)
stopwords:
- "HUMAN:"
- "### Response:"
# define chat roles
roles:
  user: "HUMAN:"
  system: "GPT:"
template:
  # template file ".tmpl" with the prompt template to use by default on the endpoint call. Note there is no extension in the files
  completion: completion
  chat: ggml-gpt4all-j
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
    chat: ggml-gpt4all-j
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
   chat: ggml-gpt4all-j
```

See also [chatbot-ui](https://github.com/go-skynet/LocalAI/tree/master/examples/chatbot-ui) as an example on how to use config files.

### Full config model file reference

```yaml
name: gpt-3.5-turbo

# Default model parameters
parameters:
  # Relative to the models path
  model: ggml-gpt4all-j
  # temperature
  temperature: 0.3
  # all the OpenAI request options here..
  top_k: 
  top_p: 
  max_tokens:
  batch:
  f16: true
  ignore_eos: true
  n_keep: 10
  seed: 
  mode: 
  step: 

# Default context size
context_size: 512
# Default number of threads
threads: 10
# Define a backend (optional). By default it will try to guess the backend the first time the model is interacted with.
backend: gptj # available: llama, stablelm, gpt2, gptj rwkv
# stopwords (if supported by the backend)
stopwords:
- "HUMAN:"
- "### Response:"
# string to trim space to
trimspace:
- string
# Strings to cut from the response
cutstrings:
- "string"
# define chat roles
roles:
  user: "HUMAN:"
  system: "GPT:"
  assistant: "ASSISTANT:"
template:
  # template file ".tmpl" with the prompt template to use by default on the endpoint call. Note there is no extension in the files
  completion: completion
  chat: ggml-gpt4all-j
  edit: edit_template

# Enable F16 if backend supports it
f16: true
# Enable debugging
debug: true
# Enable embeddings
embeddings: true
# Mirostat configuration (llama.cpp only)
mirostat_eta: 0.8
mirostat_tau: 0.9
mirostat: 1

# GPU Layers (only used when built with cublas)
gpu_layers: 22

# Directory used to store additional assets (used for stablediffusion)
asset_dir: ""
```
</details>

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

### CLI

You can control LocalAI with command line arguments, to specify a binding address, or the number of threads.

<details>

Usage:

```
local-ai --models-path <model_path> [--address <address>] [--threads <num_threads>]
```

| Parameter    | Environment Variable | Default Value | Description                            |
| ------------ | -------------------- | ------------- | -------------------------------------- |
| models-path        | MODELS_PATH           |               | The path where you have models (ending with `.bin`).      |
| threads      | THREADS              | Number of Physical cores     | The number of threads to use for text generation. |
| address      | ADDRESS              | :8080         | The address and port to listen on. |
| context-size | CONTEXT_SIZE         | 512           | Default token context size. |
| debug | DEBUG         | false           | Enable debug mode. |
| config-file | CONFIG_FILE         | empty           | Path to a LocalAI config file. |
| upload_limit | UPLOAD_LIMIT         | 5MB           | Upload limit for whisper. |
| image-path | IMAGE_PATH         | empty           | Image directory to store and serve processed images. |

</details>

## Setup

Currently LocalAI comes as a container image and can be used with docker or a container engine of choice. You can check out all the available images with corresponding tags [here](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest).

### Docker

<details>
Example of starting the API with `docker`:

```bash
docker run -p 8080:8080 -ti --rm quay.io/go-skynet/local-ai:latest --models-path /path/to/models --context-size 700 --threads 4
```

You should see:
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

Note: the binary inside the image is rebuild at the start of the container to enable CPU optimizations for the execution environment, you can set the environment variable `REBUILD` to `false` to prevent this behavior.

</details>

### Build locally

<details>

In order to build the `LocalAI` container image locally you can use `docker`:

```
# build the image
docker build -t LocalAI .
docker run LocalAI
```

Or you can build the binary with `make`:

```
make build
```

</details>

### Build on mac

Building on Mac (M1 or M2) works, but you may need to install some prerequisites using `brew`. 

<details>

The below has been tested by one mac user and found to work. Note that this doesn't use docker to run the server:

```
# install build dependencies
brew install cmake
brew install go

# clone the repo
git clone https://github.com/go-skynet/LocalAI.git

cd LocalAI

# build the binary
make build

# Download gpt4all-j to models/
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# Use a template from the examples
cp -rf prompt-templates/ggml-gpt4all-j.tmpl models/

# Run LocalAI
./local-ai --models-path ./models/ --debug

# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "ggml-gpt4all-j",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9 
   }'
```

</details>

### Build with Image generation support

<details>

**Requirements**: OpenCV, Gomp

Image generation is experimental and requires `GO_TAGS=stablediffusion` to be set during build:

```
make GO_TAGS=stablediffusion rebuild
```

</details>

### Accelleration

#### OpenBLAS

<details>

Requirements: OpenBLAS

```
make BUILD_TYPE=openblas build
```

</details>

#### CuBLAS

<details>

Requirement: Nvidia CUDA toolkit

Note: CuBLAS support is experimental, and has not been tested on real HW. please report any issues you find!

```
make BUILD_TYPE=cublas build
```

More informations available in the upstream PR: https://github.com/ggerganov/llama.cpp/pull/1412

</details>

### Windows compatibility

It should work, however you need to make sure you give enough resources to the container. See https://github.com/go-skynet/LocalAI/issues/2

### Run LocalAI in Kubernetes

LocalAI can be installed inside Kubernetes with helm.

<details>

1. Add the helm repo
    ```bash
    helm repo add go-skynet https://go-skynet.github.io/helm-charts/
    ```
1. Create a values files with your settings:
```bash
cat <<EOF > values.yaml
deployment:
  image: quay.io/go-skynet/local-ai:latest
  env:
    threads: 4
    contextSize: 1024
    modelsPath: "/models"
# Optionally create a PVC, mount the PV to the LocalAI Deployment,
# and download a model to prepopulate the models directory
modelsVolume:
  enabled: true
  url: "https://gpt4all.io/models/ggml-gpt4all-j.bin"
  pvc:
    size: 6Gi
    accessModes:
    - ReadWriteOnce
  auth:
    # Optional value for HTTP basic access authentication header
    basic: "" # 'username:password' base64 encoded
service:
  type: ClusterIP
  annotations: {}
  # If using an AWS load balancer, you'll need to override the default 60s load balancer idle timeout
  # service.beta.kubernetes.io/aws-load-balancer-connection-idle-timeout: "1200"
EOF
```
3. Install the helm chart:
```bash
helm repo update
helm install local-ai go-skynet/local-ai -f values.yaml
```

Check out also the [helm chart repository on GitHub](https://github.com/go-skynet/helm-charts).

</details>

## Supported OpenAI API endpoints

You can check out the [OpenAI API reference](https://platform.openai.com/docs/api-reference/chat/create). 

Following the list of endpoints/parameters supported. 

Note:

- You can also specify the model as part of the OpenAI token.
- If only one model is available, the API will use it for all the requests.

### Chat completions

<details>
For example, to generate a chat completion, you can send a POST request to the `/v1/chat/completions` endpoint with the instruction as the request body:

```
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "ggml-koala-7b-model-q4_0-r2.bin",
     "messages": [{"role": "user", "content": "Say this is a test!"}],
     "temperature": 0.7
   }'
```

Available additional parameters: `top_p`, `top_k`, `max_tokens`
</details>

### Edit completions

<details>
To generate an edit completion you can send a POST request to the `/v1/edits` endpoint with the instruction as the request body:

```
curl http://localhost:8080/v1/edits -H "Content-Type: application/json" -d '{
     "model": "ggml-koala-7b-model-q4_0-r2.bin",
     "instruction": "rephrase",
     "input": "Black cat jumped out of the window",
     "temperature": 0.7
   }'
```

Available additional parameters: `top_p`, `top_k`, `max_tokens`.

</details>

### Completions

<details>

To generate a completion, you can send a POST request to the `/v1/completions` endpoint with the instruction as per the request body:

```
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "ggml-koala-7b-model-q4_0-r2.bin",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

Available additional parameters: `top_p`, `top_k`, `max_tokens`

</details>

### List models

<details>
You can list all the models available with:

```
curl http://localhost:8080/v1/models
```

</details>

### Embeddings

OpenAI docs: https://platform.openai.com/docs/api-reference/embeddings

<details>

The embedding endpoint is experimental and enabled only if the model is configured with `embeddings: true` in its `yaml` file, for example:

```yaml
name: text-embedding-ada-002
parameters:
  model: bert
embeddings: true
backend: "bert-embeddings"
```

There is an example available [here](https://github.com/go-skynet/LocalAI/tree/master/examples/query_data/).

Note: embeddings is supported only with `llama.cpp` compatible models and `bert` models. bert is more performant and available independently of the LLM model.

</details>

### Transcriptions endpoint

<details>

Note: requires ffmpeg in the container image, which is currently not shipped due to licensing issues. We will prepare separated images with ffmpeg. (stay tuned!)

Download one of the models from https://huggingface.co/ggerganov/whisper.cpp/tree/main in the `models` folder, and create a YAML file for your model:

```yaml
name: whisper-1
backend: whisper
parameters:
  model: whisper-en
```

The transcriptions endpoint then can be tested like so:
```
wget --quiet --show-progress -O gb1.ogg https://upload.wikimedia.org/wikipedia/commons/1/1f/George_W_Bush_Columbia_FINAL.ogg

curl http://localhost:8080/v1/audio/transcriptions -H "Content-Type: multipart/form-data" -F file="@$PWD/gb1.ogg" -F model="whisper-1"                                                     

{"text":"My fellow Americans, this day has brought terrible news and great sadness to our country.At nine o'clock this morning, Mission Control in Houston lost contact with our Space ShuttleColumbia.A short time later, debris was seen falling from the skies above Texas.The Columbia's lost.There are no survivors.One board was a crew of seven.Colonel Rick Husband, Lieutenant Colonel Michael Anderson, Commander Laurel Clark, Captain DavidBrown, Commander William McCool, Dr. Kultna Shavla, and Elon Ramon, a colonel in the IsraeliAir Force.These men and women assumed great risk in the service to all humanity.In an age when spaceflight has come to seem almost routine, it is easy to overlook thedangers of travel by rocket and the difficulties of navigating the fierce outer atmosphere ofthe Earth.These astronauts knew the dangers, and they faced them willingly, knowing they had a highand noble purpose in life.Because of their courage and daring and idealism, we will miss them all the more.All Americans today are thinking as well of the families of these men and women who havebeen given this sudden shock and grief.You're not alone.Our entire nation agrees with you, and those you loved will always have the respect andgratitude of this country.The cause in which they died will continue.Mankind has led into the darkness beyond our world by the inspiration of discovery andthe longing to understand.Our journey into space will go on.In the skies today, we saw destruction and tragedy.As farther than we can see, there is comfort and hope.In the words of the prophet Isaiah, \"Lift your eyes and look to the heavens who createdall these, he who brings out the starry hosts one by one and calls them each by name.\"Because of his great power and mighty strength, not one of them is missing.The same creator who names the stars also knows the names of the seven souls we mourntoday.The crew of the shuttle Columbia did not return safely to Earth yet we can pray that all aresafely home.May God bless the grieving families and may God continue to bless America.[BLANK_AUDIO]"}
```

</details>
  
### Image generation

OpenAI docs: https://platform.openai.com/docs/api-reference/images/create

LocalAI supports generating images with Stable diffusion, running on CPU.

| mode=0                                                                                                                | mode=1 (winograd/sgemm)                                                                                                                |
|------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| ![test](https://github.com/go-skynet/LocalAI/assets/2420543/7145bdee-4134-45bb-84d4-f11cb08a5638)                      | ![b643343452981](https://github.com/go-skynet/LocalAI/assets/2420543/abf14de1-4f50-4715-aaa4-411d703a942a)          |
| ![b6441997879](https://github.com/go-skynet/LocalAI/assets/2420543/d50af51c-51b7-4f39-b6c2-bf04c403894c)              | ![winograd2](https://github.com/go-skynet/LocalAI/assets/2420543/1935a69a-ecce-4afc-a099-1ac28cb649b3)                |
| ![winograd](https://github.com/go-skynet/LocalAI/assets/2420543/1979a8c4-a70d-4602-95ed-642f382f6c6a)                | ![winograd3](https://github.com/go-skynet/LocalAI/assets/2420543/e6d184d4-5002-408f-b564-163986e1bdfb)                |

<details>

To generate an image you can send a POST request to the `/v1/images/generations` endpoint with the instruction as the request body:

```bash
# 512x512 is supported too
curl http://localhost:8080/v1/images/generations -H "Content-Type: application/json" -d '{
            "prompt": "A cute baby sea otter",
            "size": "256x256" 
          }'
```

Available additional parameters: `mode`, `step`.

Note: To set a negative prompt, you can split the prompt with `|`, for instance: `a cute baby sea otter|malformed`.

```bash
curl http://localhost:8080/v1/images/generations -H "Content-Type: application/json" -d '{
            "prompt": "floating hair, portrait, ((loli)), ((one girl)), cute face, hidden hands, asymmetrical bangs, beautiful detailed eyes, eye shadow, hair ornament, ribbons, bowties, buttons, pleated skirt, (((masterpiece))), ((best quality)), colorful|((part of the head)), ((((mutated hands and fingers)))), deformed, blurry, bad anatomy, disfigured, poorly drawn face, mutation, mutated, extra limb, ugly, poorly drawn hands, missing limb, blurry, floating limbs, disconnected limbs, malformed hands, blur, out of focus, long neck, long body, Octane renderer, lowres, bad anatomy, bad hands, text",
            "size": "256x256"
          }'
```

Note: image generator supports images up to 512x512. You can use other tools however to upscale the image, for instance: https://github.com/upscayl/upscayl.

#### Setup

Note: In order to use the `images/generation` endpoint, you need to build LocalAI with `GO_TAGS=stablediffusion`.

1. Create a model file `stablediffusion.yaml` in the models folder:

```yaml
name: stablediffusion
backend: stablediffusion
asset_dir: stablediffusion_assets
```
2. Create a `stablediffusion_assets` directory inside your `models` directory
3. Download the ncnn assets from https://github.com/EdVince/Stable-Diffusion-NCNN#out-of-box and place them in `stablediffusion_assets`.

The models directory should look like the following:

```
models
├── stablediffusion_assets
│   ├── AutoencoderKL-256-256-fp16-opt.param
│   ├── AutoencoderKL-512-512-fp16-opt.param
│   ├── AutoencoderKL-base-fp16.param
│   ├── AutoencoderKL-encoder-512-512-fp16.bin
│   ├── AutoencoderKL-fp16.bin
│   ├── FrozenCLIPEmbedder-fp16.bin
│   ├── FrozenCLIPEmbedder-fp16.param
│   ├── log_sigmas.bin
│   ├── tmp-AutoencoderKL-encoder-256-256-fp16.param
│   ├── UNetModel-256-256-MHA-fp16-opt.param
│   ├── UNetModel-512-512-MHA-fp16-opt.param
│   ├── UNetModel-base-MHA-fp16.param
│   ├── UNetModel-MHA-fp16.bin
│   └── vocab.txt
└── stablediffusion.yaml
```

</details>

## LocalAI API endpoints

Besides the OpenAI endpoints, there are additional LocalAI-only API endpoints.

### Applying a model - `/models/apply`

This endpoint can be used to install a model in runtime. 

<details>

LocalAI will create a batch process that downloads the required files from a model definition and automatically reload itself to include the new model. 

Input: `url`, `name` (optional), `files` (optional)

```bash
curl http://localhost:8080/models/apply -H "Content-Type: application/json" -d '{
     "url": "<MODEL_DEFINITION_URL>",
     "name": "<MODEL_NAME>",
     "files": [
        {
            "uri": "<additional_file>",
            "sha256": "<additional_file_hash>",
            "name": "<additional_file_name>"
        }
     ]
   }
```

An optional, list of additional files can be specified to be downloaded. The `name` allows to override the model name.

Returns an `uuid` and an `url` to follow up the state of the process:

```json
{ "uid":"251475c9-f666-11ed-95e0-9a8a4480ac58", "status":"http://localhost:8080/models/jobs/251475c9-f666-11ed-95e0-9a8a4480ac58"}
```

To see a collection example of curated models definition files, see the [model-gallery](https://github.com/go-skynet/model-gallery).

</details>

### Inquiry model job state `/models/jobs/<uid>`

This endpoint returns the state of the batch job associated to a model
<details>

This endpoint can be used with the uuid returned by `/models/apply` to check a job state:

```bash
curl http://localhost:8080/models/jobs/251475c9-f666-11ed-95e0-9a8a4480ac58
```

Returns a json containing the error, and if the job is being processed:

```json
{"error":null,"processed":true,"message":"completed"}
```

</details>

## Clients

OpenAI clients are already compatible with LocalAI by overriding the basePath, or the target URL.

## Javascript

<details> 

https://github.com/openai/openai-node/

```javascript
import { Configuration, OpenAIApi } from 'openai';

const configuration = new Configuration({
  basePath: `http://localhost:8080/v1`
});
const openai = new OpenAIApi(configuration);
```

</details>

## Python

<details>

https://github.com/openai/openai-python

Set the `OPENAI_API_BASE` environment variable, or by code:

```python
import openai

openai.api_base = "http://localhost:8080/v1"

# create a chat completion
chat_completion = openai.ChatCompletion.create(model="gpt-3.5-turbo", messages=[{"role": "user", "content": "Hello world"}])

# print the completion
print(completion.choices[0].message.content)
```

</details>

## Frequently asked questions

Here are answers to some of the most common questions.


### How do I get models? 

<details>

Most ggml-based models should work, but newer models may require additions to the API. If a model doesn't work, please feel free to open up issues. However, be cautious about downloading models from the internet and directly onto your machine, as there may be security vulnerabilities in lama.cpp or ggml that could be maliciously exploited. Some models can be found on Hugging Face: https://huggingface.co/models?search=ggml, or models from gpt4all should also work: https://github.com/nomic-ai/gpt4all.

</details>

### What's the difference with Serge, or XXX?


<details>

LocalAI is a multi-model solution that doesn't focus on a specific model type (e.g., llama.cpp or alpaca.cpp), and it handles all of these internally for faster inference,  easy to set up locally and deploy to Kubernetes.

</details>


### Can I use it with a Discord bot, or XXX?

<details>

Yes! If the client uses OpenAI and supports setting a different base URL to send requests to, you can use the LocalAI endpoint. This allows to use this with every application that was supposed to work with OpenAI, but without changing the application!

</details>


### Can this leverage GPUs? 

<details>

There is partial GPU support, see build instructions above.

</details>

### Where is the webUI? 

<details> 
There is the availability of localai-webui and chatbot-ui in the examples section and can be setup as per the instructions. However as LocalAI is an API you can already plug it into existing projects that provides are UI interfaces to OpenAI's APIs. There are several already on github, and should be compatible with LocalAI already (as it mimics the OpenAI API)

</details>

### Does it work with AutoGPT? 

<details>

AutoGPT currently doesn't allow to set a different API URL, but there is a PR open for it, so this should be possible soon!

</details>

## Projects already using LocalAI to run local models

Feel free to open up a PR to get your project listed!

- [Kairos](https://github.com/kairos-io/kairos)
- [k8sgpt](https://github.com/k8sgpt-ai/k8sgpt#running-local-models)
- [Spark](https://github.com/cedriking/spark)

## Blog posts and other articles

- https://medium.com/@tyler_97636/k8sgpt-localai-unlock-kubernetes-superpowers-for-free-584790de9b65
- https://kairos.io/docs/examples/localai/

## Short-term roadmap

- [x] Mimic OpenAI API (https://github.com/go-skynet/LocalAI/issues/10)
- [ ] Binary releases (https://github.com/go-skynet/LocalAI/issues/6)
- [ ] Upstream our golang bindings to llama.cpp (https://github.com/ggerganov/llama.cpp/issues/351) and [gpt4all](https://github.com/go-skynet/LocalAI/issues/85)
- [x] Multi-model support
- [x] Have a webUI!
- [x] Allow configuration of defaults for models.
- [x] Support for embeddings
- [x] Support for audio transcription with https://github.com/ggerganov/whisper.cpp
- [ ] GPU/CUDA support ( https://github.com/go-skynet/LocalAI/issues/69 )
- [ ] Enable automatic downloading of models from a curated gallery, with only free-licensed models, directly from the webui.

## Star history

[![LocalAI Star history Chart](https://api.star-history.com/svg?repos=go-skynet/LocalAI&type=Date)](https://star-history.com/#go-skynet/LocalAI&Date)

## License

LocalAI is a community-driven project. It was initially created by [Ettore Di Giacinto](https://github.com/mudler/) at the [SpectroCloud OSS Office](https://github.com/spectrocloud).

MIT

## Golang bindings used

- [go-skynet/go-llama.cpp](https://github.com/go-skynet/go-llama.cpp)
- [go-skynet/go-gpt4all-j.cpp](https://github.com/go-skynet/go-gpt4all-j.cpp)
- [go-skynet/go-gpt2.cpp](https://github.com/go-skynet/go-gpt2.cpp)
- [go-skynet/go-bert.cpp](https://github.com/go-skynet/go-bert.cpp)
- [donomii/go-rwkv.cpp](https://github.com/donomii/go-rwkv.cpp)

## Acknowledgements

LocalAI couldn't have been built without the help of great software already available from the community. Thank you!

- [llama.cpp](https://github.com/ggerganov/llama.cpp)
- https://github.com/tatsu-lab/stanford_alpaca
- https://github.com/cornelk/llama-go for the initial ideas
- https://github.com/antimatter15/alpaca.cpp
- https://github.com/EdVince/Stable-Diffusion-NCNN
- https://github.com/ggerganov/whisper.cpp
- https://github.com/saharNooby/rwkv.cpp

## Contributors

<a href="https://github.com/go-skynet/LocalAI/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=go-skynet/LocalAI" />
</a>
