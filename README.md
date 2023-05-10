<h1 align="center">
  <br>
  <img height="300" src="https://user-images.githubusercontent.com/2420543/233147843-88697415-6dbf-4368-a862-ab217f9f7342.jpeg"> <br>
    LocalAI
<br>
</h1>

[![tests](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml) [![build container images](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml)

[![](https://dcbadge.vercel.app/api/server/uJAeKSAGDy?style=flat-square&theme=default-inverted)](https://discord.gg/uJAeKSAGDy) 

**LocalAI** is a drop-in replacement REST API compatible with OpenAI for local CPU inferencing. It allows to run models locally or on-prem with consumer grade hardware, supporting multiple models families. Supports also GPT4ALL-J which is licensed under Apache 2.0.

- OpenAI compatible API
- Supports multiple models
- Once loaded the first time, it keep models loaded in memory for faster inference
- Support for prompt templates
- Doesn't shell-out, but uses C bindings for a faster inference and better performance. 

LocalAI is a community-driven project, focused on making the AI accessible to anyone. Any contribution, feedback and PR is welcome! It was initially created by [mudler](https://github.com/mudler/) at the [SpectroCloud OSS Office](https://github.com/spectrocloud).

LocalAI uses C++ bindings for optimizing speed. It is based on [llama.cpp](https://github.com/ggerganov/llama.cpp), [gpt4all](https://github.com/nomic-ai/gpt4all), [rwkv.cpp](https://github.com/saharNooby/rwkv.cpp), [ggml](https://github.com/ggerganov/ggml), [whisper.cpp](https://github.com/ggerganov/whisper.cpp) for audio transcriptions, and [bert.cpp](https://github.com/skeskinen/bert.cpp) for embedding.

See [examples on how to integrate LocalAI](https://github.com/go-skynet/LocalAI/tree/master/examples/).

## News

- 10-05-2023: __1.8.0__ released! 🔥 Added support for fast and accurate embeddings with `bert.cpp` ( https://github.com/go-skynet/LocalAI/pull/222 )
- 09-05-2023: Added experimental support for transcriptions endpoint ( https://github.com/go-skynet/LocalAI/pull/211 )
- 08-05-2023: Support for embeddings with models using the `llama.cpp` backend ( https://github.com/go-skynet/LocalAI/pull/207 )
- 02-05-2023: Support for `rwkv.cpp` models ( https://github.com/go-skynet/LocalAI/pull/158 ) and for `/edits` endpoint
- 01-05-2023: Support for SSE stream of tokens in `llama.cpp` backends ( https://github.com/go-skynet/LocalAI/pull/152 )

Twitter: [@LocalAI_API](https://twitter.com/LocalAI_API) and [@mudler_it](https://twitter.com/mudler_it)

### Blogs and articles

- [Tutorial to use k8sgpt with LocalAI](https://medium.com/@tyler_97636/k8sgpt-localai-unlock-kubernetes-superpowers-for-free-584790de9b65) - excellent usecase for localAI, using AI to analyse Kubernetes clusters.

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
- [GPT4ALL](https://github.com/nomic-ai/gpt4all) (changes required, see below)
- [GPT4ALL-J](https://gpt4all.io/models/ggml-gpt4all-j.bin) (no changes required)
- Koala
- [cerebras-GPT with ggml](https://huggingface.co/lxe/Cerebras-GPT-2.7B-Alpaca-SP-ggml)
- WizardLM
- [RWKV](https://github.com/BlinkDL/RWKV-LM) models with [rwkv.cpp](https://github.com/saharNooby/rwkv.cpp)

### GPT4ALL

Note: You might need to convert older models to the new format, see [here](https://github.com/ggerganov/llama.cpp#using-gpt4all) for instance to run `gpt4all`.

### RWKV

<details>

A full example on how to run a rwkv model is in the [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/rwkv).

Note: rwkv models have an associated tokenizer along that needs to be provided with it:

```
36464540 -rw-r--r--  1 mudler mudler 1.2G May  3 10:51 rwkv_small
36464543 -rw-r--r--  1 mudler mudler 2.4M May  3 10:51 rwkv_small.tokenizer.json
```

</details>

### Others

It should also be compatible with StableLM and GPTNeoX ggml models (untested).

### Hardware requirements

Depending on the model you are attempting to run might need more RAM or CPU resources. Check out also [here](https://github.com/ggerganov/llama.cpp#memorydisk-requirements) for `ggml` based backends. `rwkv` is less expensive on resources.


## Usage

> `LocalAI` comes by default as a container image. You can check out all the available images with corresponding tags [here](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest).

The easiest way to run LocalAI is by using `docker-compose`:

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
docker-compose up -d --build

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
docker-compose up -d --build

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

To build locally, run `make build` (see below).

### Other examples

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

<details>

The embedding endpoint is experimental and enabled only if the model is configured with `embeddings: true` in its `yaml` file, for example:

```yaml
name: text-embedding-ada-002
parameters:
  model: bert
embeddings: true
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

Not currently, as ggml doesn't support GPUs yet: https://github.com/ggerganov/llama.cpp/discussions/915.

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

- [llama.cpp](https://github.com/ggerganov/llama.cpp)
- https://github.com/tatsu-lab/stanford_alpaca
- https://github.com/cornelk/llama-go for the initial ideas
- https://github.com/antimatter15/alpaca.cpp for the light model version (this is compatible and tested only with that checkpoint model!)

## Contributors

<a href="https://github.com/go-skynet/LocalAI/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=go-skynet/LocalAI" />
</a>
