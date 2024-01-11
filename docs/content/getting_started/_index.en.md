 
+++
disableToc = false
title = "Getting started"
weight = 1
url = '/basics/getting_started/'
+++

`LocalAI` is available as a container image and binary. It can be used with docker, podman, kubernetes and any container engine. 
Container images are published to [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest) and [Dockerhub](https://hub.docker.com/r/localai/localai).

[<img src="https://img.shields.io/badge/dockerhub-images-important.svg?logo=Docker">](https://hub.docker.com/r/localai/localai)
[<img src="https://img.shields.io/badge/quay.io-images-important.svg?">](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest)

See also our [How to](https://io.midori-ai.xyz/howtos/) section for end-to-end guided examples curated by the community.

### How to get started

The easiest way to run LocalAI is by using [`docker compose`](https://docs.docker.com/compose/install/) or with [Docker](https://docs.docker.com/engine/install/) (to build locally, see the [build section]({{%relref "build" %}})).

LocalAI needs at least a model file to work, or a configuration YAML file, or both. You can customize further model defaults and specific settings with a configuration file (see [advanced]({{%relref "advanced" %}})).

{{% notice note %}}
To run with GPU Accelleration, see [GPU acceleration]({{%relref "features/gpu-acceleration" %}}).
{{% /notice %}}

{{< tabs >}}
{{% tab name="Docker" %}}

```bash
# Prepare the models into the `model` directory
mkdir models

# copy your models to it
cp your-model.gguf models/

# run the LocalAI container
docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:latest --models-path /models --context-size 700 --threads 4
# You should see:
# 
# ┌───────────────────────────────────────────────────┐
# │                   Fiber v2.42.0                   │
# │               http://127.0.0.1:8080               │
# │       (bound on host 0.0.0.0 and port 8080)       │
# │                                                   │
# │ Handlers ............. 1  Processes ........... 1 │
# │ Prefork ....... Disabled  PID ................. 1 │
# └───────────────────────────────────────────────────┘

# Try the endpoint with curl
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.gguf",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

{{% notice note %}}
- If running on Apple Silicon (ARM) it is **not** suggested to run on Docker due to emulation. Follow the [build instructions]({{%relref "build" %}}) to use Metal acceleration for full GPU support.
- If you are running Apple x86_64 you can use `docker`, there is no additional gain into building it from source.
{{% /notice %}}

{{% /tab %}}
{{% tab name="Docker compose" %}}

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# copy your models to models/
cp your-model.gguf models/

# (optional) Edit the .env file to set things like context size and threads
# vim .env

# start with docker compose
docker compose up -d --pull always
# or you can build the images with:
# docker compose up -d --build

# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"your-model.gguf","object":"model"}]}

curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.gguf",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

Note: If you are on Windows, please make sure the project is on the Linux Filesystem, otherwise loading models might be slow. For more Info: [Microsoft Docs](https://learn.microsoft.com/en-us/windows/wsl/filesystems)

{{% /tab %}}

{{% tab name="Kubernetes" %}}

For installing LocalAI in Kubernetes, you can use the following helm chart:

```bash
# Install the helm repository
helm repo add go-skynet https://go-skynet.github.io/helm-charts/
# Update the repositories
helm repo update
# Get the values
helm show values go-skynet/local-ai > values.yaml

# Edit the values value if needed
# vim values.yaml ...

# Install the helm chart
helm install local-ai go-skynet/local-ai -f values.yaml
```

{{% /tab %}}
{{% tab name="From binary" %}}

LocalAI binary releases are available in [Github](https://github.com/go-skynet/LocalAI/releases).

{{% /tab %}}

{{% tab name="From source" %}}

See the [build section]({{%relref "build" %}}).
  
{{% /tab %}}

{{< /tabs >}}

### Running Popular models (one-click!)

You can run `local-ai` directly with a model name, and it will download the model and start the API with the model loaded.

> Don't need GPU acceleration? use the CPU images which are lighter and do not have Nvidia dependencies
> To know which version of CUDA do you have available, you can check with `nvidia-smi` or `nvcc --version`


{{< tabs >}}
{{% tab name="CPU-only" %}}

| Model | Category | Docker command |
| --- | --- | --- |
| [phi-2](https://huggingface.co/microsoft/phi-2) | LLM | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core phi-2``` |
| [llava](https://github.com/SkunkworksAI/BakLLaVA) | Multimodal LLM | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core llava``` |
| [mistral-openorca](https://huggingface.co/Open-Orca/Mistral-7B-OpenOrca) | LLM | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core mistral-openorca``` |
| [bert-cpp](https://github.com/skeskinen/bert.cpp) | Embeddings | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core bert-cpp``` |
| [all-minilm-l6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2) | Embeddings | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg all-minilm-l6-v2``` |
| whisper-base | Audio to Text | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core whisper-base``` |
| rhasspy-voice-en-us-amy | Text to Audio | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core rhasspy-voice-en-us-amy``` |
| coqui | Text to Audio | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg coqui``` |
| bark | Text to Audio | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg bark``` |
| vall-e-x | Text to Audio | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg vall-e-x``` |
| mixtral-instruct Mixtral-8x7B-Instruct-v0.1 | LLM | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core mixtral-instruct``` |
| [tinyllama-chat](https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF) [original model](https://huggingface.co/TinyLlama/TinyLlama-1.1B-Chat-v0.3) | LLM | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core tinyllama-chat``` |
| [dolphin-2.5-mixtral-8x7b](https://huggingface.co/TheBloke/dolphin-2.5-mixtral-8x7b-GGUF) | LLM | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core dolphin-2.5-mixtral-8x7b``` |
{{% /tab %}}
{{% tab name="GPU (CUDA 11)" %}}



| Model | Category | Docker command |
| --- | --- | --- |
| [phi-2](https://huggingface.co/microsoft/phi-2) | LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core phi-2``` |
| [llava](https://github.com/SkunkworksAI/BakLLaVA) | Multimodal LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core llava``` |
| [mistral-openorca](https://huggingface.co/Open-Orca/Mistral-7B-OpenOrca) | LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core mistral-openorca``` |
| [bert-cpp](https://github.com/skeskinen/bert.cpp) | Embeddings | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core bert-cpp``` |
| [all-minilm-l6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2) | Embeddings | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 all-minilm-l6-v2``` |
| whisper-base | Audio to Text | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core whisper-base``` |
| rhasspy-voice-en-us-amy | Text to Audio | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core rhasspy-voice-en-us-amy``` |
| coqui | Text to Audio | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 coqui``` |
| bark | Text to Audio | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 bark``` |
| vall-e-x | Text to Audio | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 vall-e-x``` |
| mixtral-instruct Mixtral-8x7B-Instruct-v0.1 | LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core mixtral-instruct``` |
| [tinyllama-chat](https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF) [original model](https://huggingface.co/TinyLlama/TinyLlama-1.1B-Chat-v0.3) | LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core tinyllama-chat``` |
| [dolphin-2.5-mixtral-8x7b](https://huggingface.co/TheBloke/dolphin-2.5-mixtral-8x7b-GGUF) | LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core dolphin-2.5-mixtral-8x7b``` |
{{% /tab %}}


{{% tab name="GPU (CUDA 12)" %}}

| Model | Category | Docker command |
| --- | --- | --- |
| [phi-2](https://huggingface.co/microsoft/phi-2) | LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core phi-2``` |
| [llava](https://github.com/SkunkworksAI/BakLLaVA) | Multimodal LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core llava``` |
| [mistral-openorca](https://huggingface.co/Open-Orca/Mistral-7B-OpenOrca) | LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core mistral-openorca``` |
| [bert-cpp](https://github.com/skeskinen/bert.cpp) | Embeddings | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core bert-cpp``` |
| [all-minilm-l6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2) | Embeddings | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 all-minilm-l6-v2``` |
| whisper-base | Audio to Text | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core whisper-base``` |
| rhasspy-voice-en-us-amy | Text to Audio | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core rhasspy-voice-en-us-amy``` |
| coqui | Text to Audio | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 coqui``` |
| bark | Text to Audio | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 bark``` |
| vall-e-x | Text to Audio | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 vall-e-x``` |
| mixtral-instruct Mixtral-8x7B-Instruct-v0.1 | LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core mixtral-instruct``` |
| [tinyllama-chat](https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF) [original model](https://huggingface.co/TinyLlama/TinyLlama-1.1B-Chat-v0.3) | LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core tinyllama-chat``` |
| [dolphin-2.5-mixtral-8x7b](https://huggingface.co/TheBloke/dolphin-2.5-mixtral-8x7b-GGUF) | LLM | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core dolphin-2.5-mixtral-8x7b``` |
{{% /tab %}}

{{< /tabs >}}

{{% notice note %}}

LocalAI can be started (either the container image or the binary) with a list of model config files URLs or our short-handed format (e.g. `huggingface://`. `github://`). It works by passing the urls as arguments or environment variable, for example:

```
local-ai github://owner/repo/file.yaml@branch

# Env
MODELS="github://owner/repo/file.yaml@branch,github://owner/repo/file.yaml@branch" local-ai

# Args
local-ai --models github://owner/repo/file.yaml@branch --models github://owner/repo/file.yaml@branch
```

For example, to start localai with phi-2, it's possible for instance to also use a full config file from gists:

```bash
docker run -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core https://gist.githubusercontent.com/mudler/ad601a0488b497b69ec549150d9edd18/raw/a8a8869ef1bb7e3830bf5c0bae29a0cce991ff8d/phi-2.yaml
```

The file should be a valid LocalAI YAML configuration file, for the full syntax see [advanced]({{%relref "advanced" %}}).
{{% /notice %}}

### Container images

LocalAI has a set of images to support CUDA, ffmpeg and 'vanilla' (CPU-only). The image are on [quay](https://quay.io/repository/go-skynet/local-ai?tab=tags) and [Dockerhub](https://hub.docker.com/r/localai/localai).

Images have the following tags depending on the dependencies included in the images. Images ending with `-core` are smaller images without predownload python dependencies.

{{< tabs >}}
{{% tab name="Vanilla / CPU Images" %}}
- `master`
- `latest`
- `{{< version >}}`
- `{{< version >}}-ffmpeg`
- `{{< version >}}-ffmpeg-core`

Core Images - Smaller images without predownload python dependencies
{{% /tab %}}

{{% tab name="GPU Images CUDA 11" %}}

Images with Nvidia accelleration support

> If you do not know which version of CUDA do you have available, you can check with `nvidia-smi` or `nvcc --version`

- `master-cublas-cuda11`
- `master-cublas-cuda11-core`
- `{{< version >}}-cublas-cuda11`
- `{{< version >}}-cublas-cuda11-core`
- `{{< version >}}-cublas-cuda11-ffmpeg`
- `{{< version >}}-cublas-cuda11-ffmpeg-core`

Core Images - Smaller images without predownload python dependencies
{{% /tab %}}

{{% tab name="GPU Images CUDA 12" %}}

Images with Nvidia accelleration support

> If you do not know which version of CUDA do you have available, you can check with `nvidia-smi` or `nvcc --version`

- `master-cublas-cuda12`
- `master-cublas-cuda12-core`
- `{{< version >}}-cublas-cuda12`
- `{{< version >}}-cublas-cuda12-core`
- `{{< version >}}-cublas-cuda12-ffmpeg`
- `{{< version >}}-cublas-cuda12-ffmpeg-core`

Core Images - Smaller images without predownload python dependencies

{{% /tab %}}

{{< /tabs >}}

Example:

- Standard (GPT + `stablediffusion`): `quay.io/go-skynet/local-ai:latest`
- FFmpeg: `quay.io/go-skynet/local-ai:{{< version >}}-ffmpeg`
- CUDA 11+FFmpeg: `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11-ffmpeg`
- CUDA 12+FFmpeg: `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12-ffmpeg`

{{% notice note %}}
Note: the binary inside the image is pre-compiled, and might not suite all CPUs.
To enable CPU optimizations for the execution environment,
the default behavior is to rebuild when starting the container.
To disable this auto-rebuild behavior,
set the environment variable `REBUILD` to `false`.

See [docs on all environment variables]({{%relref "advanced#environment-variables" %}})
for more info.
{{% /notice %}}

### Example: Use luna-ai-llama2 model with `docker`

```bash
mkdir models

# Download luna-ai-llama2 to models/
wget https://huggingface.co/TheBloke/Luna-AI-Llama2-Uncensored-GGUF/resolve/main/luna-ai-llama2-uncensored.Q4_0.gguf -O models/luna-ai-llama2

# Use a template from the examples
cp -rf prompt-templates/getting_started.tmpl models/luna-ai-llama2.tmpl

docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:latest --models-path /models --context-size 700 --threads 4

# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"luna-ai-llama2","object":"model"}]}

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "luna-ai-llama2",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9
   }'

# {"model":"luna-ai-llama2","choices":[{"message":{"role":"assistant","content":"I'm doing well, thanks. How about you?"}}]}
```

To see other model configurations, see also the example section [here](https://github.com/mudler/LocalAI/tree/master/examples/configurations).

### Examples

![Screenshot from 2023-04-26 23-59-55](https://user-images.githubusercontent.com/2420543/234715439-98d12e03-d3ce-4f94-ab54-2b256808e05e.png)

To see other examples on how to integrate with other projects for instance for question answering or for using it with chatbot-ui, see: [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/).

