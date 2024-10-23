+++
disableToc = false
title = "Quickstart"
weight = 3
url = '/basics/getting_started/'
icon = "rocket_launch"
+++

**LocalAI** is a free, open-source alternative to OpenAI (Anthropic, etc.), functioning as a drop-in replacement REST API for local inferencing. It allows you to run [LLMs]({{% relref "docs/features/text-generation" %}}), generate images, and produce audio, all locally or on-premises with consumer-grade hardware, supporting multiple model families and architectures.

{{% alert icon="💡" %}}

**Security considerations**

If you are exposing LocalAI remotely, make sure you protect the API endpoints adequately with a mechanism which allows to protect from the incoming traffic or alternatively, run LocalAI with `API_KEY` to gate the access with an API key. The API key guarantees a total access to the features (there is no role separation), and it is to be considered as likely as an admin role.

To access the WebUI with an API_KEY, browser extensions such as [Requestly](https://requestly.com/) can be used (see also https://github.com/mudler/LocalAI/issues/2227#issuecomment-2093333752). See also [API flags]({{% relref "docs/advanced/advanced-usage#api-flags" %}}) for the flags / options available when starting LocalAI.

{{% /alert %}}

## Using the Bash Installer

Install LocalAI easily using the bash installer with the following command:

```sh
curl https://localai.io/install.sh | sh
```

For a full list of options, refer to the [Installer Options]({{% relref "docs/advanced/installer" %}}) documentation.

Binaries can also be [manually downloaded]({{% relref "docs/reference/binaries" %}}).

## Using Homebrew on MacOS

{{% alert icon="⚠️" %}}
The Homebrew formula currently doesn't have the same options than the bash script
{{% /alert %}}

You can install Homebrew's [LocalAI](https://formulae.brew.sh/formula/localai) with the following command:

```
brew install localai
```


## Using Container Images or Kubernetes

LocalAI is available as a container image compatible with various container engines such as Docker, Podman, and Kubernetes. Container images are published on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest) and [Docker Hub](https://hub.docker.com/r/localai/localai).

For detailed instructions, see [Using container images]({{% relref "docs/getting-started/container-images" %}}). For Kubernetes deployment, see [Run with Kubernetes]({{% relref "docs/getting-started/kubernetes" %}}).

## Running LocalAI with All-in-One (AIO) Images

> _Already have a model file? Skip to [Run models manually]({{% relref "docs/getting-started/models" %}})_.

LocalAI's All-in-One (AIO) images are pre-configured with a set of models and backends to fully leverage almost all the features of LocalAI. If pre-configured models are not required, you can use the standard [images]({{% relref "docs/getting-started/container-images" %}}).

These images are available for both CPU and GPU environments. AIO images are designed for ease of use and require no additional configuration.

It is recommended to use AIO images if you prefer not to configure the models manually or via the web interface. For running specific models, refer to the [manual method]({{% relref "docs/getting-started/models" %}}).

The AIO images come pre-configured with the following features:
- Text to Speech (TTS)
- Speech to Text
- Function calling
- Large Language Models (LLM) for text generation
- Image generation
- Embedding server

For instructions on using AIO images, see [Using container images]({{% relref "docs/getting-started/container-images#all-in-one-images" %}}).

## What's Next?

There is much more to explore with LocalAI! You can run any model from Hugging Face, perform video generation, and also voice cloning. For a comprehensive overview, check out the [features]({{% relref "docs/features" %}}) section.

Explore additional resources and community contributions:

- [Installer Options]({{% relref "docs/advanced/installer" %}})
- [Run from Container images]({{% relref "docs/getting-started/container-images" %}})
- [Examples to try from the CLI]({{% relref "docs/getting-started/try-it-out" %}})
- [Build LocalAI and the container image]({{% relref "docs/getting-started/build" %}})
- [Run models manually]({{% relref "docs/getting-started/models" %}})
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples#examples)
