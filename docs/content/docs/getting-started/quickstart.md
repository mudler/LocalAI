 
+++
disableToc = false
title = "Quickstart"
weight = 3
url = '/basics/getting_started/'
icon = "rocket_launch"

+++

**LocalAI** is the free, Open Source alternative to OpenAI (Anthropic, ...), acting as a drop-in replacement REST API for local inferencing. It allows you to run [LLMs]({{%relref "docs/features/text-generation" %}}), generate images, and audio, all locally or on-prem with consumer-grade hardware, supporting multiple model families and architectures.

## Installation

### Using the Bash Installer

You can easily install LocalAI using the bash installer with the following command:

```
curl https://localai.io/install.sh | sh
```

See also the [Installer Options]({{%relref "docs/advanced/installer" %}}) for the full list of options.

Binaries can be also [manually downloaded]({{%relref "docs/reference/binaries" %}}).

### Using Container Images

LocalAI is available as a container image compatible with various container engines like Docker, Podman, and Kubernetes. Container images are published on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest) and [Docker Hub](https://hub.docker.com/r/localai/localai).

See: [Using container images]({{%relref "docs/getting-started/container-images" %}})

### Running LocalAI with All-in-One (AIO) Images

> _Do you have already a model file? Skip to [Run models manually]({{%relref "docs/getting-started/manual" %}})_.

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

See: [Using container images]({{%relref "docs/getting-started/container-images" %}}) for instructions on how to use AIO images.


## What's next?

There is much more to explore! run any model from huggingface, video generation, and voice cloning with LocalAI, check out the [features]({{%relref "docs/features" %}}) section for a full overview.

Explore further resources and community contributions:

- [Try it out]({{%relref "docs/getting-started/try-it-out" %}})
- [Build LocalAI and the container image]({{%relref "docs/getting-started/build" %}})
- [Run models manually]({{%relref "docs/getting-started/manual" %}})
- [Installer Options]({{%relref "docs/advanced/installer" %}})
- [Run from Container images]({{%relref "docs/getting-started/container-images" %}})
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)
