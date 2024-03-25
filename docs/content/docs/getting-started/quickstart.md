 
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

LocalAI's All-in-One (AIO) images are pre-configured with a set of models and backends to fully leverage almost all the LocalAI featureset. 

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
# docker run -p 8080:8080 --gpus all --name local-ai -ti localai/localai:latest-aio-gpu-cuda-11
# docker run -p 8080:8080 --gpus all --name local-ai -ti localai/localai:latest-aio-gpu-cuda-12
```


Or with a docker-compose file:

```yaml
version: "3.9"
services:
  api:
    image: localai/localai:{{< version >}}-aio-cpu
    # For Nvidia GPUs decomment one of the following (cuda11 or cuda12):
    # image: localai/localai:{{< version >}}-aio-gpu-cuda-11
    # image: localai/localai:{{< version >}}-aio-gpu-cuda-12
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/readyz"]
      interval: 1m
      timeout: 120m
      retries: 120
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

For a list of all the container-images available, see [Container images]({{%relref "docs/reference/container-images" %}}). To learn more about All-in-one images instead, see [All-in-one Images]({{%relref "docs/reference/aio-images" %}}).

## What's next?

Explore further resources and community contributions:

- [Build LocalAI and the container image]({{%relref "docs/getting-started/build" %}})
- [Run models manually]({{%relref "docs/getting-started/manual" %}})
- [Run other models]({{%relref "docs/getting-started/run-other-models" %}})
- [Container images]({{%relref "docs/reference/container-images" %}})
- [All-in-one Images]({{%relref "docs/reference/aio-images" %}})
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)