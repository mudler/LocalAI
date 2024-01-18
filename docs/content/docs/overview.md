
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

[<img src="https://img.shields.io/badge/dockerhub-images-important.svg?logo=Docker">](https://hub.docker.com/r/localai/localai)
[<img src="https://img.shields.io/badge/quay.io-images-important.svg?">](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest)

> 💡 Get help - [❓FAQ](https://localai.io/faq/) [❓How tos](https://io.midori-ai.xyz/howtos/) [💭Discussions](https://github.com/go-skynet/LocalAI/discussions) [💭Discord](https://discord.gg/uJAeKSAGDy)
>
> [💻 Quickstart](https://localai.io/basics/getting_started/) [📣 News](https://localai.io/basics/news/) [ 🛫 Examples ](https://github.com/go-skynet/LocalAI/tree/master/examples/) [ 🖼️ Models ](https://localai.io/models/) [ 🚀 Roadmap ](https://github.com/mudler/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3Aroadmap)

**LocalAI** is the free, Open Source OpenAI alternative. LocalAI act as a drop-in replacement REST API that's compatible with OpenAI API specifications for local inferencing. It allows you to run LLMs, generate images, audio (and not only) locally or on-prem with consumer grade hardware, supporting multiple model families and architectures. Does not require GPU. It is maintained by [mudler](https://github.com/mudler).

<p align="center">
<a href="https://twitter.com/LocalAI_API" target="blank">
<img src="https://img.shields.io/twitter/follow/LocalAI_API?label=Follow: LocalAI_API&style=social" alt="Follow LocalAI_API"/>
</a>
<a href="https://discord.gg/uJAeKSAGDy" target="blank">
<img src="https://dcbadge.vercel.app/api/server/uJAeKSAGDy?style=flat-square&theme=default-inverted" alt="Join LocalAI Discord Community"/>
</a>

In a nutshell:

- Local, OpenAI drop-in alternative REST API. You own your data.
- NO GPU required. NO Internet access is required either
  - Optional, GPU Acceleration is available. See also the [build section](https://localai.io/basics/build/index.html).
- Supports multiple models
- 🏃 Once loaded the first time, it keep models loaded in memory for faster inference
- ⚡ Doesn't shell-out, but uses bindings for a faster inference and better performance.

LocalAI is focused on making the AI accessible to anyone. Any contribution, feedback and PR is welcome!

Note that this started just as a fun weekend project by [mudler](https://github.com/mudler) in order to try to create the necessary pieces for a full AI assistant like `ChatGPT`: the community is growing fast and we are working hard to make it better and more stable. If you want to help, please consider contributing (see below)!


## 🚀 Features

- 📖 [Text generation with GPTs](https://localai.io/features/text-generation/) (`llama.cpp`, `gpt4all.cpp`, ... [:book: and more](https://localai.io/model-compatibility/index.html#model-compatibility-table))
- 🗣 [Text to Audio](https://localai.io/features/text-to-audio/)
- 🔈 [Audio to Text](https://localai.io/features/audio-to-text/) (Audio transcription with `whisper.cpp`)
- 🎨 [Image generation with stable diffusion](https://localai.io/features/image-generation)
- 🔥 [OpenAI functions](https://localai.io/features/openai-functions/) 🆕
- 🧠 [Embeddings generation for vector databases](https://localai.io/features/embeddings/)
- ✍️ [Constrained grammars](https://localai.io/features/constrained_grammars/)
- 🖼️ [Download Models directly from Huggingface ](https://localai.io/models/)
- 🆕 [Vision API](https://localai.io/features/gpt-vision/)

## How does it work?

LocalAI is an API written in Go that serves as an OpenAI shim, enabling software already developed with OpenAI SDKs to seamlessly integrate with LocalAI. It can be effortlessly implemented as a substitute, even on consumer-grade hardware. This capability is achieved by employing various C++ backends, including [ggml](https://github.com/ggerganov/ggml), to perform inference on LLMs using both CPU and, if desired, GPU. Internally LocalAI backends are just gRPC server, indeed you can specify and build your own gRPC server and extend LocalAI in runtime as well. It is possible to specify external gRPC server and/or binaries that LocalAI will manage internally.

LocalAI uses a mixture of backends written in various languages (C++, Golang, Python, ...). You can check [the model compatibility table]({{%relref "docs/reference/compatibility-table" %}}) to learn about all the components of LocalAI.

![localai](https://github.com/go-skynet/localai-website/assets/2420543/6492e685-8282-4217-9daa-e229a31548bc)

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
- https://github.com/cmp-nct/ggllm.cpp



## Backstory

As much as typical open source projects starts, I, [mudler](https://github.com/mudler/), was fiddling around with [llama.cpp](https://github.com/ggerganov/llama.cpp) over my long nights and wanted to have a way to call it from `go`, as I am a Golang developer and use it extensively. So I've created `LocalAI` (or what was initially known as `llama-cli`) and added an API to it.

But guess what? The more I dived into this rabbit hole, the more I realized that I had stumbled upon something big. With all the fantastic C++ projects floating around the community, it dawned on me that I could piece them together to create a full-fledged OpenAI replacement. So, ta-da! LocalAI was born, and it quickly overshadowed its humble origins.

Now, why did I choose to go with C++ bindings, you ask? Well, I wanted to keep LocalAI snappy and lightweight, allowing it to run like a champ on any system and avoid any Golang penalties of the GC, and, most importantly built on shoulders of giants like `llama.cpp`. Go is good at backends and API and is easy to maintain. And hey, don't forget that I'm all about sharing the love. That's why I made LocalAI MIT licensed, so everyone can hop on board and benefit from it.

As if that wasn't exciting enough, as the project gained traction, [mkellerman](https://github.com/mkellerman) and [Aisuko](https://github.com/Aisuko) jumped in to lend a hand. mkellerman helped set up some killer examples, while Aisuko is becoming our community maestro. The community now is growing even more with new contributors and users, and I couldn't be happier about it!

Oh, and let's not forget the real MVP here—[llama.cpp](https://github.com/ggerganov/llama.cpp). Without this extraordinary piece of software, LocalAI wouldn't even exist. So, a big shoutout to the community for making this magic happen!

## 🤗 Contributors

This is a community project, a special thanks to our contributors! 🤗
<a href="https://github.com/go-skynet/LocalAI/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=go-skynet/LocalAI" />
</a>
<a href="https://github.com/go-skynet/LocalAI-website/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=go-skynet/LocalAI-website" />
</a>
