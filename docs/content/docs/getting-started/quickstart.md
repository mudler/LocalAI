 
+++
disableToc = false
title = "Quickstart"
weight = 3
url = '/basics/getting_started/'
icon = "rocket_launch"

+++

**LocalAI** is the free, Open Source OpenAI alternative. LocalAI act as a drop-in replacement REST API that's compatible with OpenAI API specifications for local inferencing. It allows you to run [LLMs]({{%relref "docs/features/text-generation" %}}), generate images, audio (and not only) locally or on-prem with consumer grade hardware, supporting multiple model families and architectures.

## Installation Methods

LocalAI is available as a container image and binary, compatible with various container engines like Docker, Podman, and Kubernetes. Container images are published on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest) and [Docker Hub](https://hub.docker.com/r/localai/localai). Binaries can be downloaded from [GitHub](https://github.com/mudler/LocalAI/releases).


{{% alert icon="💡" %}}

**Hardware Requirements:** The hardware requirements for LocalAI vary based on the model size and quantization method used. For performance benchmarks with different backends, such as `llama.cpp`, visit [this link](https://github.com/ggerganov/llama.cpp#memorydisk-requirements). The `rwkv` backend is noted for its lower resource consumption.

{{% /alert %}}

## Prerequisites

Before you begin, ensure you have a container engine installed if you are not using the binaries. Suitable options include Docker or Podman. For installation instructions, refer to the following guides:

- [Install Docker Desktop (Mac, Windows, Linux)](https://docs.docker.com/get-docker/)
- [Install Podman (Linux)](https://podman.io/getting-started/installation)
- [Install Docker engine (Servers)](https://docs.docker.com/engine/install/#get-started)


## Running Models

> _Do you have already a model file? Skip to [Run models manually]({{%relref "docs/getting-started/manual" %}})_.

LocalAI allows one-click runs with popular models. It downloads the model and starts the API with the model loaded. 

There are different categories of models: [LLMs]({{%relref "docs/features/text-generation" %}}), [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) , [Embeddings]({{%relref "docs/features/embeddings" %}}), [Audio to Text]({{%relref "docs/features/audio-to-text" %}}), and [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) depending on the backend being used and the model architecture.

{{% alert icon="💡" %}}

To customize the models, see [Model customization]({{%relref "docs/getting-started/customize-model" %}}). For more model configurations, visit the [Examples Section](https://github.com/mudler/LocalAI/tree/master/examples/configurations) and the configurations for the models below is available [here](https://github.com/mudler/LocalAI/tree/master/embedded/models).
{{% /alert %}}

{{< tabs tabTotal="3" >}}
{{% tab tabName="CPU-only" %}}

> 💡Don't need GPU acceleration? use the CPU images which are lighter and do not have Nvidia dependencies

| Model | Category | Docker command |
| --- | --- | --- |
| [phi-2](https://huggingface.co/microsoft/phi-2) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core phi-2``` |
| 🌋 [llava](https://github.com/SkunkworksAI/BakLLaVA) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core llava``` |
| [mistral-openorca](https://huggingface.co/Open-Orca/Mistral-7B-OpenOrca) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core mistral-openorca``` |
| [bert-cpp](https://github.com/skeskinen/bert.cpp) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core bert-cpp``` |
| [all-minilm-l6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg all-minilm-l6-v2``` |
| whisper-base | [Audio to Text]({{%relref "docs/features/audio-to-text" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core whisper-base``` |
| rhasspy-voice-en-us-amy | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core rhasspy-voice-en-us-amy``` |
| 🐸 [coqui](https://github.com/coqui-ai/TTS) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg coqui``` |
| 🐶 [bark](https://github.com/suno-ai/bark) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg bark``` |
| 🔊 [vall-e-x](https://github.com/Plachtaa/VALL-E-X)  | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg vall-e-x``` |
| mixtral-instruct Mixtral-8x7B-Instruct-v0.1 | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core mixtral-instruct``` |
| [tinyllama-chat](https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF) [original model](https://huggingface.co/TinyLlama/TinyLlama-1.1B-Chat-v0.3) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core tinyllama-chat``` |
| [dolphin-2.5-mixtral-8x7b](https://huggingface.co/TheBloke/dolphin-2.5-mixtral-8x7b-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core dolphin-2.5-mixtral-8x7b``` |
| 🐍 [mamba](https://github.com/state-spaces/mamba) | [LLM]({{%relref "docs/features/text-generation" %}}) | GPU-only |
| animagine-xl | [Text to Image]({{%relref "docs/features/image-generation" %}}) | GPU-only |
| transformers-tinyllama | [LLM]({{%relref "docs/features/text-generation" %}}) | GPU-only |
| [codellama-7b](https://huggingface.co/codellama/CodeLlama-7b-hf) (with transformers) | [LLM]({{%relref "docs/features/text-generation" %}}) | GPU-only |
| [codellama-7b-gguf](https://huggingface.co/TheBloke/CodeLlama-7B-GGUF) (with llama.cpp) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core codellama-7b-gguf``` |
{{% /tab %}}
{{% tab tabName="GPU (CUDA 11)" %}}


> To know which version of CUDA do you have available, you can check with `nvidia-smi` or `nvcc --version` see also [GPU acceleration]({{%relref "docs/features/gpu-acceleration" %}}).

| Model | Category | Docker command |
| --- | --- | --- |
| [phi-2](https://huggingface.co/microsoft/phi-2) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core phi-2``` |
| 🌋 [llava](https://github.com/SkunkworksAI/BakLLaVA) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core llava``` |
| [mistral-openorca](https://huggingface.co/Open-Orca/Mistral-7B-OpenOrca) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core mistral-openorca``` |
| [bert-cpp](https://github.com/skeskinen/bert.cpp) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core bert-cpp``` |
| [all-minilm-l6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 all-minilm-l6-v2``` |
| whisper-base | [Audio to Text]({{%relref "docs/features/audio-to-text" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core whisper-base``` |
| rhasspy-voice-en-us-amy | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core rhasspy-voice-en-us-amy``` |
| 🐸 [coqui](https://github.com/coqui-ai/TTS) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 coqui``` |
| 🐶 [bark](https://github.com/suno-ai/bark) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 bark``` |
| 🔊 [vall-e-x](https://github.com/Plachtaa/VALL-E-X) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 vall-e-x``` |
| mixtral-instruct Mixtral-8x7B-Instruct-v0.1 | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core mixtral-instruct``` |
| [tinyllama-chat](https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF) [original model](https://huggingface.co/TinyLlama/TinyLlama-1.1B-Chat-v0.3) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core tinyllama-chat``` |
| [dolphin-2.5-mixtral-8x7b](https://huggingface.co/TheBloke/dolphin-2.5-mixtral-8x7b-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core dolphin-2.5-mixtral-8x7b``` |
| 🐍 [mamba](https://github.com/state-spaces/mamba) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 mamba-chat``` |
| animagine-xl | [Text to Image]({{%relref "docs/features/image-generation" %}}) |  ```docker run -ti -p 8080:8080 -e COMPEL=0 --gpus all localai/localai:{{< version >}}-cublas-cuda11 animagine-xl``` |
| transformers-tinyllama | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 transformers-tinyllama``` |
| [codellama-7b](https://huggingface.co/codellama/CodeLlama-7b-hf) | [LLM]({{%relref "docs/features/text-generation" %}})  | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 codellama-7b``` |
| [codellama-7b-gguf](https://huggingface.co/TheBloke/CodeLlama-7B-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}})  | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core codellama-7b-gguf``` |
{{% /tab %}}


{{% tab tabName="GPU (CUDA 12)" %}}

> To know which version of CUDA do you have available, you can check with `nvidia-smi` or `nvcc --version` see also [GPU acceleration]({{%relref "docs/features/gpu-acceleration" %}}).

| Model | Category | Docker command |
| --- | --- | --- |
| [phi-2](https://huggingface.co/microsoft/phi-2) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core phi-2``` |
| 🌋 [llava](https://github.com/SkunkworksAI/BakLLaVA) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core llava``` |
| [mistral-openorca](https://huggingface.co/Open-Orca/Mistral-7B-OpenOrca) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core mistral-openorca``` |
| [bert-cpp](https://github.com/skeskinen/bert.cpp) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core bert-cpp``` |
| [all-minilm-l6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 all-minilm-l6-v2``` |
| whisper-base | [Audio to Text]({{%relref "docs/features/audio-to-text" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core whisper-base``` |
| rhasspy-voice-en-us-amy | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core rhasspy-voice-en-us-amy``` |
| 🐸 [coqui](https://github.com/coqui-ai/TTS) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 coqui``` |
| 🐶 [bark](https://github.com/suno-ai/bark) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 bark``` |
| 🔊 [vall-e-x](https://github.com/Plachtaa/VALL-E-X) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 vall-e-x``` |
| mixtral-instruct Mixtral-8x7B-Instruct-v0.1 | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core mixtral-instruct``` |
| [tinyllama-chat](https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF) [original model](https://huggingface.co/TinyLlama/TinyLlama-1.1B-Chat-v0.3) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core tinyllama-chat``` |
| [dolphin-2.5-mixtral-8x7b](https://huggingface.co/TheBloke/dolphin-2.5-mixtral-8x7b-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core dolphin-2.5-mixtral-8x7b``` |
| 🐍 [mamba](https://github.com/state-spaces/mamba) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 mamba-chat``` |
| animagine-xl | [Text to Image]({{%relref "docs/features/image-generation" %}}) | ```docker run -ti -p 8080:8080 -e COMPEL=0 --gpus all localai/localai:{{< version >}}-cublas-cuda12 animagine-xl``` |
| transformers-tinyllama | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 transformers-tinyllama``` |
| [codellama-7b](https://huggingface.co/codellama/CodeLlama-7b-hf) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 codellama-7b``` |
| [codellama-7b-gguf](https://huggingface.co/TheBloke/CodeLlama-7B-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}})  | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core codellama-7b-gguf``` |
{{% /tab %}}

{{< /tabs >}}

{{% alert icon="💡" %}}
**Tip** You can actually specify multiple models to start an instance with the models loaded, for example to have both llava and phi-2 configured:

```bash
docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core llava phi-2
```

{{% /alert %}}

## Container images

LocalAI provides a variety of images to support different environments. These images are available on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags) and [Docker Hub](https://hub.docker.com/r/localai/localai).

For GPU Acceleration support for Nvidia video graphic cards, use the Nvidia/CUDA images, if you don't have a GPU, use the CPU images. If you have AMD or Mac Silicon, see the [build section]({{%relref "docs/getting-started/build" %}}).

{{% alert icon="💡" %}}

**Available Images Types**:

- Images ending with `-core` are smaller images without predownload python dependencies. Use these images if you plan to use `llama.cpp`, `stablediffusion-ncn`, `tinydream` or `rwkv` backends - if you are not sure which one to use, do **not** use these images.
- FFMpeg is **not** included in the default images due to [its licensing](https://www.ffmpeg.org/legal.html). If you need FFMpeg, use the images ending with `-ffmpeg`. Note that `ffmpeg` is needed in case of using `audio-to-text` LocalAI's features.
- If using old and outdated CPUs and no GPUs you might need to set `REBUILD` to `true` as environment variable along with options to disable the flags which your CPU does not support, however note that inference will perform poorly and slow. See also [flagset compatibility]({{%relref "docs/getting-started/build#cpu-flagset-compatibility" %}}).

{{% /alert %}}

{{< tabs tabTotal="3" >}}
{{% tab tabName="Vanilla / CPU Images" %}}

| Description | Quay | Docker Hub                                   |
| --- | --- |-----------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master` | `localai/localai:master`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest` | `localai/localai:latest`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}` | `localai/localai:{{< version >}}`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-ffmpeg` | `localai/localai:{{< version >}}-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-ffmpeg-core` | `localai/localai:{{< version >}}-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="GPU Images CUDA 11" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-cublas-cuda11` | `localai/localai:master-cublas-cuda11`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-cublas-cuda11` | `localai/localai:latest-cublas-cuda11`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11` | `localai/localai:{{< version >}}-cublas-cuda11`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11-ffmpeg` | `localai/localai:{{< version >}}-cublas-cuda11-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11-ffmpeg-core` | `localai/localai:{{< version >}}-cublas-cuda11-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="GPU Images CUDA 12" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-cublas-cuda12` | `localai/localai:master-cublas-cuda12`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-cublas-cuda12` | `localai/localai:latest-cublas-cuda12`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12` | `localai/localai:{{< version >}}-cublas-cuda12`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12-ffmpeg` | `localai/localai:{{< version >}}-cublas-cuda12-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12-ffmpeg-core` | `localai/localai:{{< version >}}-cublas-cuda12-ffmpeg-core` |

{{% /tab %}}

{{< /tabs >}}

## What's next?

Explore further resources and community contributions:

- [Community How to's](https://io.midori-ai.xyz/howtos/)
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)

[![Screenshot from 2023-04-26 23-59-55](https://user-images.githubusercontent.com/2420543/234715439-98d12e03-d3ce-4f94-ab54-2b256808e05e.png)](https://github.com/mudler/LocalAI/tree/master/examples#examples)
