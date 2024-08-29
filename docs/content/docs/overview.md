
+++
title = "Overview"
weight = 1
toc = true
description = "What is LocalAI?"
tags = ["Beginners"]
categories = [""]
author = "Ettore Di Giacinto"
# This allows to overwrite the landing page
url = '/'
icon = "info"
+++

<p align="center">
<a href="https://localai.io"><img width=512 src="https://github.com/go-skynet/LocalAI/assets/2420543/0966aa2a-166e-4f99-a3e5-6c915fc997dd"></a>
</p               >

<p align="center">
<a href="https://github.com/go-skynet/LocalAI/fork" target="blank">
<img src="https://img.shields.io/github/forks/go-skynet/LocalAI?style=for-the-badge" alt="LocalAI forks"/>
</a>
<a href="https://github.com/go-skynet/LocalAI/stargazers" target="blank">
<img src="https://img.shields.io/github/stars/go-skynet/LocalAI?style=for-the-badge" alt="LocalAI stars"/>
</a>
<a href="https://github.com/go-skynet/LocalAI/pulls" target="blank">
<img src="https://img.shields.io/github/issues-pr/go-skynet/LocalAI?style=for-the-badge" alt="LocalAI pull-requests"/>
</a>
<a href='https://github.com/go-skynet/LocalAI/releases'>
<img src='https://img.shields.io/github/release/go-skynet/LocalAI?&label=Latest&style=for-the-badge'>
</a>
</p>

<p align="center">
<a href="https://hub.docker.com/r/localai/localai" target="blank">
<img src="https://img.shields.io/badge/dockerhub-images-important.svg?logo=Docker" alt="LocalAI Docker hub"/>
</a>
<a href="https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest" target="blank">
<img src="https://img.shields.io/badge/quay.io-images-important.svg?" alt="LocalAI Quay.io"/>
</a>
</p>

<p align="center">
<a href="https://twitter.com/LocalAI_API" target="blank">
<img src="https://img.shields.io/twitter/follow/LocalAI_API?label=Follow: LocalAI_API&style=social" alt="Follow LocalAI_API"/>
</a>
<a href="https://discord.gg/uJAeKSAGDy" target="blank">
<img src="https://dcbadge.vercel.app/api/server/uJAeKSAGDy?style=flat-square&theme=default-inverted" alt="Join LocalAI Discord Community"/>
</a>
</p>

> 💡 Get help - [❓FAQ](https://localai.io/faq/) [💭Discussions](https://github.com/go-skynet/LocalAI/discussions) [💭Discord](https://discord.gg/uJAeKSAGDy)
>
> [💻 Quickstart](https://localai.io/basics/getting_started/) [🖼️ Models](https://models.localai.io/) [🚀 Roadmap](https://github.com/mudler/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3Aroadmap) [🥽 Demo](https://demo.localai.io) [🌍 Explorer](https://explorer.localai.io) [🛫 Examples](https://github.com/go-skynet/LocalAI/tree/master/examples/) 


**LocalAI** is the free, Open Source OpenAI alternative. LocalAI act as a drop-in replacement REST API that's compatible with OpenAI API specifications for local inferencing. It allows you to run LLMs, generate images, audio (and not only) locally or on-prem with consumer grade hardware, supporting multiple model families and architectures. Does not require GPU. It is created and maintained by [Ettore Di Giacinto](https://github.com/mudler).


## Start LocalAI

Start the image with Docker to have a functional clone of OpenAI! 🚀:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest-aio-cpu
# Do you have a Nvidia GPUs? Use this instead
# CUDA 11
# docker run -p 8080:8080 --gpus all --name local-ai -ti localai/localai:latest-aio-gpu-nvidia-cuda-11
# CUDA 12
# docker run -p 8080:8080 --gpus all --name local-ai -ti localai/localai:latest-aio-gpu-nvidia-cuda-12
```

Or just use the bash installer:

```bash
curl https://localai.io/install.sh | sh
```

See the [💻 Quickstart](https://localai.io/basics/getting_started/) for all the options and way you can run LocalAI!

## What is LocalAI?

In a nutshell:

- Local, OpenAI drop-in alternative REST API. You own your data.
- NO GPU required. NO Internet access is required either
  - Optional, GPU Acceleration is available. See also the [build section](https://localai.io/basics/build/index.html).
- Supports multiple models
- 🏃 Once loaded the first time, it keep models loaded in memory for faster inference
- ⚡ Doesn't shell-out, but uses bindings for a faster inference and better performance.

LocalAI is focused on making the AI accessible to anyone. Any contribution, feedback and PR is welcome!

Note that this started just as a fun weekend project by [mudler](https://github.com/mudler) in order to try to create the necessary pieces for a full AI assistant like `ChatGPT`: the community is growing fast and we are working hard to make it better and more stable. If you want to help, please consider contributing (see below)!

### 🚀 Features

- 📖 [Text generation with GPTs](https://localai.io/features/text-generation/) (`llama.cpp`, `gpt4all.cpp`, ... [:book: and more](https://localai.io/model-compatibility/index.html#model-compatibility-table))
- 🗣 [Text to Audio](https://localai.io/features/text-to-audio/)
- 🔈 [Audio to Text](https://localai.io/features/audio-to-text/) (Audio transcription with `whisper.cpp`)
- 🎨 [Image generation with stable diffusion](https://localai.io/features/image-generation)
- 🔥 [OpenAI functions](https://localai.io/features/openai-functions/) 🆕
- 🧠 [Embeddings generation for vector databases](https://localai.io/features/embeddings/)
- ✍️ [Constrained grammars](https://localai.io/features/constrained_grammars/)
- 🖼️ [Download Models directly from Huggingface ](https://localai.io/models/)
- 🥽 [Vision API](https://localai.io/features/gpt-vision/)
- 💾 [Stores](https://localai.io/stores)
- 📈 [Reranker](https://localai.io/features/reranker/)
- 🆕🖧 [P2P Inferencing](https://localai.io/features/distribute/)

## Contribute and help

To help the project you can:

- If you have technological skills and want to contribute to development, have a look at the open issues. If you are new you can have a look at the [good-first-issue](https://github.com/go-skynet/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22) and [help-wanted](https://github.com/go-skynet/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3A%22help+wanted%22) labels.

- If you don't have technological skills you can still help improving documentation or [add examples](https://github.com/go-skynet/LocalAI/tree/master/examples) or share your user-stories with our community, any help and contribution is welcome!

## 🌟 Star history

[![LocalAI Star history Chart](https://api.star-history.com/svg?repos=go-skynet/LocalAI&type=Date)](https://star-history.com/#go-skynet/LocalAI&Date)

## 📖 License

LocalAI is a community-driven project created by [Ettore Di Giacinto](https://github.com/mudler/).

MIT - Author Ettore Di Giacinto

## 🙇 Acknowledgements

LocalAI couldn't have been built without the help of great software already available from the community. Thank you!

- [llama.cpp](https://github.com/ggerganov/llama.cpp)
- https://github.com/tatsu-lab/stanford_alpaca
- https://github.com/cornelk/llama-go for the initial ideas
- https://github.com/antimatter15/alpaca.cpp
- https://github.com/EdVince/Stable-Diffusion-NCNN
- https://github.com/ggerganov/whisper.cpp
- https://github.com/saharNooby/rwkv.cpp
- https://github.com/rhasspy/piper

## 🤗 Contributors

This is a community project, a special thanks to our contributors! 🤗
<a href="https://github.com/go-skynet/LocalAI/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=go-skynet/LocalAI" />
</a>
