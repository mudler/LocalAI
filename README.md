<h1 align="center">
  <br>
  <img height="300" src="https://user-images.githubusercontent.com/2420543/233147843-88697415-6dbf-4368-a862-ab217f9f7342.jpeg"> <br>
    LocalAI
<br>
</h1>

[![tests](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml) [![build container images](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml)

[![](https://dcbadge.vercel.app/api/server/uJAeKSAGDy?style=flat-square&theme=default-inverted)](https://discord.gg/uJAeKSAGDy) 

**LocalAI** is a straightforward, drop-in replacement API compatible with OpenAI for local CPU inferencing, based on [llama.cpp](https://github.com/ggerganov/llama.cpp), [gpt4all](https://github.com/nomic-ai/gpt4all) and [ggml](https://github.com/ggerganov/ggml), including support GPT4ALL-J which is licensed under Apache 2.0.

- OpenAI compatible API
- Supports multiple-models
- Once loaded the first time, it keep models loaded in memory for faster inference
- Support for prompt templates
- Doesn't shell-out, but uses C bindings for a faster inference and better performance. 

LocalAI is a community-driven project, focused on making the AI accessible to anyone. Any contribution, feedback and PR is welcome! It was initially created by [mudler](https://github.com/mudler/) at the [SpectroCloud OSS Office](https://github.com/spectrocloud).

### Socials and community chatter
- Follow [@LocalAI_API](https://twitter.com/LocalAI_API) on twitter.

- [Reddit post](https://www.reddit.com/r/selfhosted/comments/12w4p2f/localai_openai_compatible_api_to_run_llm_models/) about LocalAI.

- [Hacker news post](https://news.ycombinator.com/item?id=35726934) - help us out by voting if you like this project.

- [Tutorial to use k8sgpt with LocalAI](https://medium.com/@tyler_97636/k8sgpt-localai-unlock-kubernetes-superpowers-for-free-584790de9b65) - excellent usecase for localAI, using AI to analyse Kubernetes clusters.

## Model compatibility

It is compatible with the models supported by [llama.cpp](https://github.com/ggerganov/llama.cpp) supports also [GPT4ALL-J](https://github.com/nomic-ai/gpt4all) and [cerebras-GPT with ggml](https://huggingface.co/lxe/Cerebras-GPT-2.7B-Alpaca-SP-ggml).

Tested with:
- Vicuna
- Alpaca
- [GPT4ALL](https://github.com/nomic-ai/gpt4all)
- [GPT4ALL-J](https://gpt4all.io/models/ggml-gpt4all-j.bin)
- Koala
- [cerebras-GPT with ggml](https://huggingface.co/lxe/Cerebras-GPT-2.7B-Alpaca-SP-ggml)
- [RWKV](https://github.com/BlinkDL/RWKV-LM) with [rwkv.cpp](https://github.com/saharNooby/rwkv.cpp)

It should also be compatible with StableLM and GPTNeoX ggml models (untested)

Note: You might need to convert older models to the new format, see [here](https://github.com/ggerganov/llama.cpp#using-gpt4all) for instance to run `gpt4all`.

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

## Other examples

![Screenshot from 2023-04-26 23-59-55](https://user-images.githubusercontent.com/2420543/234715439-98d12e03-d3ce-4f94-ab54-2b256808e05e.png)

To see other examples on how to integrate with other projects for instance chatbot-ui, see: [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/).

## Prompt templates 

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

</details>

## Installation

Currently LocalAI comes as container images and can be used with docker or a containre engine of choice. 

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

## API

`LocalAI` provides an API for running text generation as a service, that follows the OpenAI reference and can be used as a drop-in. The models once loaded the first time will be kept in memory.

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
| debug | DEBUG         | false           | Enable debug mode. |
| config-file | CONFIG_FILE         | empty           | Path to a LocalAI config file. |

Once the server is running, you can start making requests to it using HTTP, using the OpenAI API. 

</details>

### Supported OpenAI API endpoints

You can check out the [OpenAI API reference](https://platform.openai.com/docs/api-reference/chat/create). 

Following the list of endpoints/parameters supported. 

Note:

- You can also specify the model as part of the OpenAI token.
- If only one model is available, the API will use it for all the requests.

#### Chat completions

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

#### Completions

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

#### List models

<details>
You can list all the models available with:

```
curl http://localhost:8080/v1/models
```

</details>

## Advanced configuration

LocalAI can be configured to serve user-defined models with a set of default parameters and templates.

<details>
You can create multiple `yaml` files in the models path or either specify a single YAML configuration file.

For instance, a configuration file (`gpt-3.5-turbo.yaml`) can be declaring the "gpt-3.5-turbo" model but backed by the "testmodel" model file:

```yaml
name: gpt-3.5-turbo
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

## Windows compatibility

It should work, however you need to make sure you give enough resources to the container. See https://github.com/go-skynet/LocalAI/issues/2

## Build locally

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

## Build on mac

Building on Mac (M1 or M2) works, but you may need to install some prerequisites using brew. The below has been tested by one mac user and found to work. Note that this doesn't use docker to run the server:

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
We are working on to have a good out of the box experience - however as LocalAI is an API you can already plug it into existing projects that provides are UI interfaces to OpenAI's APIs. There are several already on github, and should be compatible with LocalAI already (as it mimics the OpenAI API)

</details>

### Does it work with AutoGPT? 

<details>

AutoGPT currently doesn't allow to set a different API URL, but there is a PR open for it, so this should be possible soon!

</details>

## Projects already using LocalAI to run local models

Feel free to open up a PR to get your project listed!

- [Kairos](https://github.com/kairos-io/kairos)
- [k8sgpt](https://github.com/k8sgpt-ai/k8sgpt#running-local-models)

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

LocalAI is a community-driven project. It was initially created by [mudler](https://github.com/mudler/) at the [SpectroCloud OSS Office](https://github.com/spectrocloud).

MIT

## Golang bindings used

- [go-skynet/go-llama.cpp](https://github.com/go-skynet/go-llama.cpp)
- [go-skynet/go-gpt4all-j.cpp](https://github.com/go-skynet/go-gpt4all-j.cpp)
- [go-skynet/go-gpt2.cpp](https://github.com/go-skynet/go-gpt2.cpp)
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
