 
+++
disableToc = false
title = "Quickstart"
weight = 3
url = '/basics/getting_started/'
icon = "rocket_launch"

+++

**LocalAI** is the free, Open Source OpenAI alternative. LocalAI act as a drop-in replacement REST API that's compatible with OpenAI API specifications for local inferencing. It allows you to run [LLMs]({{%relref "docs/features/text-generation" %}}), generate images, audio (and not only) locally or on-prem with consumer grade hardware, supporting multiple model families and architectures.

LocalAI is available as a container image and binary, compatible with various container engines like Docker, Podman, and Kubernetes. Container images are published on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest) and [Docker Hub](https://hub.docker.com/r/localai/localai). Binaries can be downloaded from [GitHub](https://github.com/mudler/LocalAI/releases).

## Prerequisites

Before you begin, ensure you have a container engine installed if you are not using the binaries. Suitable options include Docker or Podman. For installation instructions, refer to the following guides:

- [Install Docker Desktop (Mac, Windows, Linux)](https://docs.docker.com/get-docker/)
- [Install Podman (Linux)](https://podman.io/getting-started/installation)
- [Install Docker engine (Servers)](https://docs.docker.com/engine/install/#get-started)

{{% alert icon="💡" %}}

**Hardware Requirements:** The hardware requirements for LocalAI vary based on the model size and quantization method used. For performance benchmarks with different backends, such as `llama.cpp`, visit [this link](https://github.com/ggerganov/llama.cpp#memorydisk-requirements). The `rwkv` backend is noted for its lower resource consumption.

{{% /alert %}}

## Running LocalAI with All-in-One (AIO) Images

> _Do you have already a model file? Skip to [Run models manually]({{%relref "docs/getting-started/manual" %}}) or [Run other models]({{%relref "docs/getting-started/run-other-models" %}}) to use an already-configured model_.

LocalAI's All-in-One (AIO) images are pre-configured with a set of models and backends to fully leverage almost all the LocalAI featureset. If you don't need models pre-configured, you can use the standard [images]({{%relref "docs/getting-started/container-images" %}}).

These images are available for both CPU and GPU environments. The AIO images are designed to be easy to use and requires no configuration.

It suggested to use the AIO images if you don't want to configure the models to run on LocalAI. If you want to run specific models, you can use the [manual method]({{%relref "docs/getting-started/manual" %}}).

The AIO Images comes pre-configured with the following features:
- Text to Speech (TTS)
- Speech to Text
- Function calling
- Large Language Models (LLM) for text generation
- Image generation
- Embedding server


Start the image with Docker:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest-aio-cpu
# For Nvidia GPUs:
# docker run -p 8080:8080 --gpus all --name local-ai -ti localai/localai:latest-aio-gpu-nvidia-cuda-11
# docker run -p 8080:8080 --gpus all --name local-ai -ti localai/localai:latest-aio-gpu-nvidia-cuda-12
```


Or with a docker-compose file:

```yaml
version: "3.9"
services:
  api:
    image: localai/localai:latest-aio-cpu
    # For a specific version:
    # image: localai/localai:{{< version >}}-aio-cpu
    # For Nvidia GPUs decomment one of the following (cuda11 or cuda12):
    # image: localai/localai:{{< version >}}-aio-gpu-nvidia-cuda-11
    # image: localai/localai:{{< version >}}-aio-gpu-nvidia-cuda-12
    # image: localai/localai:latest-aio-gpu-nvidia-cuda-11
    # image: localai/localai:latest-aio-gpu-nvidia-cuda-12
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/readyz"]
      interval: 1m
      timeout: 20m
      retries: 5
    ports:
      - 8080:8080
    environment:
      - DEBUG=true
      # ...
    volumes:
      - ./models:/build/models:cached
    # decomment the following piece if running with Nvidia GPUs
    # deploy:
    #   resources:
    #     reservations:
    #       devices:
    #         - driver: nvidia
    #           count: 1
    #           capabilities: [gpu]
```

For a list of all the container-images available, see [Container images]({{%relref "docs/getting-started/container-images" %}}). To learn more about All-in-one images instead, see [All-in-one Images]({{%relref "docs/getting-started/container-images" %}}).

{{% alert icon="💡" %}}

**Models caching**: The **AIO** image will download the needed models on the first run if not already present and store those in `/build/models` inside the container. The AIO models will be automatically updated with new versions of AIO images.

You can change the directory inside the container by specifying a `MODELS_PATH` environment variable (or `--models-path`). 

If you want to use a named model or a local directory, you can mount it as a volume to `/build/models`:

```bash
docker run -p 8080:8080 --name local-ai -ti -v $PWD/models:/build/models localai/localai:latest-aio-cpu
```

or associate a volume:

```bash
docker volume create localai-models
docker run -p 8080:8080 --name local-ai -ti -v localai-models:/build/models localai/localai:latest-aio-cpu
```

{{% /alert %}}

## Running LocalAI from Binaries

LocalAI binaries are available for both Linux and MacOS platforms and can be executed directly from your command line. These binaries are continuously updated and hosted on [our GitHub Releases page](https://github.com/mudler/LocalAI/releases). This method also supports Windows users via the Windows Subsystem for Linux (WSL). 

Use the following one-liner command in your terminal to download and run LocalAI on Linux or MacOS:

```bash
curl -Lo local-ai "https://github.com/mudler/LocalAI/releases/download/{{< version >}}/local-ai-$(uname -s)-$(uname -m)" && chmod +x local-ai && ./local-ai
```

Otherwise, here are the links to the binaries:

| OS | Link | 
| --- | --- |
| Linux  | [Download](https://github.com/mudler/LocalAI/releases/download/{{< version >}}/local-ai-Linux-x86_64) |
| MacOS  | [Download](https://github.com/mudler/LocalAI/releases/download/{{< version >}}/local-ai-Darwin-arm64) |


## Try it out

Connect to LocalAI, by default the WebUI should be accessible from http://localhost:8080 . You can also use 3rd party projects to interact with LocalAI as you would use OpenAI (see also [Integrations]({{%relref "docs/integrations" %}}) ). 

You can also test out the API endpoints using `curl`, examples below.

### Text Generation

Creates a model response for the given chat conversation. [OpenAI documentation](https://platform.openai.com/docs/api-reference/chat/create).

<details>

```bash
curl http://localhost:8080/v1/chat/completions \
      -H "Content-Type: application/json" \
      -d '{ "model": "gpt-4", "messages": [{"role": "user", "content": "How are you doing?", "temperature": 0.1}] }' 
```

</details>

### GPT Vision

Understand images.

<details>

```bash
curl http://localhost:8080/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d '{ 
        "model": "gpt-4-vision-preview", 
        "messages": [
          {
            "role": "user", "content": [
              {"type":"text", "text": "What is in the image?"},
              {
                "type": "image_url", 
                "image_url": {
                  "url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg" 
                }
              }
            ], 
          "temperature": 0.9
          }
        ]
      }' 
```

</details>

### Function calling

Call functions

<details>

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [
      {
        "role": "user",
        "content": "What is the weather like in Boston?"
      }
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "get_current_weather",
          "description": "Get the current weather in a given location",
          "parameters": {
            "type": "object",
            "properties": {
              "location": {
                "type": "string",
                "description": "The city and state, e.g. San Francisco, CA"
              },
              "unit": {
                "type": "string",
                "enum": ["celsius", "fahrenheit"]
              }
            },
            "required": ["location"]
          }
        }
      }
    ],
    "tool_choice": "auto"
  }'
```

</details>

### Image Generation

Creates an image given a prompt. [OpenAI documentation](https://platform.openai.com/docs/api-reference/images/create).

<details>

```bash
curl http://localhost:8080/v1/images/generations \
      -H "Content-Type: application/json" -d '{
          "prompt": "A cute baby sea otter",
          "size": "256x256"
        }'
```

</details>

### Text to speech


Generates audio from the input text. [OpenAI documentation](https://platform.openai.com/docs/api-reference/audio/createSpeech).

<details>

```bash
curl http://localhost:8080/v1/audio/speech \
  -H "Content-Type: application/json" \
  -d '{
    "model": "tts-1",
    "input": "The quick brown fox jumped over the lazy dog.",
    "voice": "alloy"
  }' \
  --output speech.mp3
```

</details>


### Audio Transcription

Transcribes audio into the input language. [OpenAI Documentation](https://platform.openai.com/docs/api-reference/audio/createTranscription).

<details>

Download first a sample to transcribe:

```bash
wget --quiet --show-progress -O gb1.ogg https://upload.wikimedia.org/wikipedia/commons/1/1f/George_W_Bush_Columbia_FINAL.ogg 
```

Send the example audio file to the transcriptions endpoint :
```bash
curl http://localhost:8080/v1/audio/transcriptions \
    -H "Content-Type: multipart/form-data" \
    -F file="@$PWD/gb1.ogg" -F model="whisper-1"
```

</details>

### Embeddings Generation

Get a vector representation of a given input that can be easily consumed by machine learning models and algorithms. [OpenAI Embeddings](https://platform.openai.com/docs/api-reference/embeddings).

<details>

```bash
curl http://localhost:8080/embeddings \
    -X POST -H "Content-Type: application/json" \
    -d '{ 
        "input": "Your text string goes here", 
        "model": "text-embedding-ada-002"
      }'
```

</details>

{{% alert icon="💡" %}}

Don't use the model file as `model` in the request unless you want to handle the prompt template for yourself.

Use the model names like you would do with OpenAI like in the examples below. For instance `gpt-4-vision-preview`, or `gpt-4`.

{{% /alert %}}

## What's next?

There is much more to explore! run any model from huggingface, video generation, and voice cloning with LocalAI, check out the [features]({{%relref "docs/features" %}}) section for a full overview.

Explore further resources and community contributions:

- [Build LocalAI and the container image]({{%relref "docs/getting-started/build" %}})
- [Run models manually]({{%relref "docs/getting-started/manual" %}})
- [Run other models]({{%relref "docs/getting-started/run-other-models" %}})
- [Container images]({{%relref "docs/getting-started/container-images" %}})
- [All-in-one Images]({{%relref "docs/getting-started/container-images" %}})
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)
