 
+++
disableToc = false
title = "Quickstart"
weight = 1
url = '/basics/getting_started/'
icon = "rocket_launch"

+++

**LocalAI** is the free, Open Source OpenAI alternative. LocalAI act as a drop-in replacement REST API that's compatible with OpenAI API specifications for local inferencing. It allows you to run LLMs, generate images, audio (and not only) locally or on-prem with consumer grade hardware, supporting multiple model families and architectures.

## Prerequisites

Before you begin, ensure you have a container engine installed if you are not using the binaries. Suitable options include Docker or Podman. For installation instructions, refer to the following guides:

- [Install Docker Desktop (Mac, Windows, Linux)](https://docs.docker.com/get-docker/)
- [Install Podman (Linux)](https://podman.io/getting-started/installation)
- [Install Docker engine (Servers)](https://docs.docker.com/engine/install/#get-started)


{{% alert icon="💡" %}}

**Hardware Requirements:** The hardware requirements for LocalAI vary based on the [model size] and [quantization] method used. For performance benchmarks with different backends, such as `llama.cpp`, visit [this link](https://github.com/ggerganov/llama.cpp#memorydisk-requirements). The `rwkv` backend is noted for its lower resource consumption.

{{% /alert %}}

## Installation Methods

LocalAI is available as a container image and binary, compatible with various container engines like Docker, Podman, and Kubernetes. Container images are published on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest) and [Dockerhub](https://hub.docker.com/r/localai/localai). Binaries can be downloaded from [GitHub](https://github.com/mudler/LocalAI/releases).


### Container images

LocalAI provides a variety of images to support different environments like CUDA, FFmpeg, and CPU-only setups. These images are available on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags) and [Dockerhub](https://hub.docker.com/r/localai/localai).

Images have the following tags depending on the dependencies included in the images. Images ending with `-core` are smaller images without predownload python dependencies.

{{< tabs tabTotal="3" >}}
{{% tab tabName="Vanilla / CPU Images" %}}
- `master`
- `latest`
- `{{< version >}}`
- `{{< version >}}-ffmpeg`
- `{{< version >}}-ffmpeg-core`

Core Images - Smaller images without predownload python dependencies
{{% /tab %}}

{{% tab tabName="GPU Images CUDA 11" %}}

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

{{% tab tabName="GPU Images CUDA 12" %}}

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

{{% alert note %}}
Note: the binary inside the image is pre-compiled, and might not suite all CPUs.
To enable CPU optimizations for the execution environment,
the default behavior is to rebuild when starting the container.
To disable this auto-rebuild behavior,
set the environment variable `REBUILD` to `false`.

See [docs on all environment variables]({{%relref "docs/advanced#environment-variables" %}})
for more info.
{{% /alert %}}

## Running Models

LocalAI allows one-click runs with popular models. It downloads the model and starts the API with the model loaded. 

> _Do you have already a model file? Skip to [Run models manually]({{%relref "docs/getting-started/manual" %}})_.

There are different categories of models: LLMs, multimodal LLMs, embeddings, audio to text, and text to audio depending on the backend being used and the model architecture.

| Category | Description |
| --- | --- |
| LLMs | Language models are models that can generate text from a prompt. |
| Multimodal LLMs | Multimodal language models are models that can generate text from a prompt and an image. |
| Embeddings | Embeddings are models that can generate embeddings(vectors of numbers) from a text. |
| Audio to Text | Audio to text models are models that can generate text from an audio file. |
| Text to Audio | Text to audio models are models that can generate audio from a text. |


{{% alert icon="💡" %}}

To customize the models, see [Model customization]({{%relref "docs/getting-started/customize-model" %}}). For more model configurations, visit the [Examples Section](https://github.com/mudler/LocalAI/tree/master/examples/configurations).
{{% /alert %}}

{{< tabs tabTotal="3" >}}
{{% tab tabName="CPU-only" %}}

> 💡Don't need GPU acceleration? use the CPU images which are lighter and do not have Nvidia dependencies

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
{{% tab tabName="GPU (CUDA 11)" %}}


> To know which version of CUDA do you have available, you can check with `nvidia-smi` or `nvcc --version` see also [GPU acceleration]({{%relref "docs/features/gpu-acceleration" %}}).

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


{{% tab tabName="GPU (CUDA 12)" %}}

> To know which version of CUDA do you have available, you can check with `nvidia-smi` or `nvcc --version` see also [GPU acceleration]({{%relref "docs/features/gpu-acceleration" %}}).

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



### What's next?

Explore further resources and community contributions:

- [Community How to's](https://io.midori-ai.xyz/howtos/)
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)

[![Screenshot from 2023-04-26 23-59-55](https://user-images.githubusercontent.com/2420543/234715439-98d12e03-d3ce-4f94-ab54-2b256808e05e.png)](https://github.com/mudler/LocalAI/tree/master/examples#examples)