<h1 align="center">
  <br>
  <img height="300" src="https://github.com/go-skynet/LocalAI/assets/2420543/0966aa2a-166e-4f99-a3e5-6c915fc997dd"> <br>
    LocalAI
<br>
</h1>

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

[![tests](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml)[![Build and Release](https://github.com/go-skynet/LocalAI/actions/workflows/release.yaml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/release.yaml)[![build container images](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml)[![Bump dependencies](https://github.com/go-skynet/LocalAI/actions/workflows/bump_deps.yaml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/bump_deps.yaml)[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/localai)](https://artifacthub.io/packages/search?repo=localai)

**LocalAI** is the free, Open Source OpenAI alternative. LocalAI act as a drop-in replacement REST API that’s compatible with OpenAI (Elevenlabs, Anthropic... ) API specifications for local AI inferencing. It allows you to run LLMs, generate images, audio (and not only) locally or on-prem with consumer grade hardware, supporting multiple model families. Does not require GPU.

## 🔥🔥 Hot topics / Roadmap

[Roadmap](https://github.com/mudler/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3Aroadmap)

- Vector store: https://github.com/mudler/LocalAI/pull/1795
- All-in-one container image: https://github.com/mudler/LocalAI/issues/1855
- Parallel function calling: https://github.com/mudler/LocalAI/pull/1726
- Upload file API: https://github.com/mudler/LocalAI/pull/1703
- Tools API support: https://github.com/mudler/LocalAI/pull/1715
- LLaVa 1.6: https://github.com/mudler/LocalAI/pull/1714
- ROCm container images: https://github.com/mudler/LocalAI/pull/1595
- Intel GPU support (sycl, transformers, diffusers): https://github.com/mudler/LocalAI/issues/1653
- Mamba support: https://github.com/mudler/LocalAI/pull/1589
- Start and share models with config file: https://github.com/mudler/LocalAI/pull/1522
- 🐸 Coqui: https://github.com/mudler/LocalAI/pull/1489
- Img2vid https://github.com/mudler/LocalAI/pull/1442

Hot topics (looking for contributors):
- Backends v2: https://github.com/mudler/LocalAI/issues/1126
- Improving UX v2: https://github.com/mudler/LocalAI/issues/1373
- Assistant API: https://github.com/mudler/LocalAI/issues/1273
- Moderation endpoint: https://github.com/mudler/LocalAI/issues/999
- Vulkan: https://github.com/mudler/LocalAI/issues/1647

If you want to help and contribute, issues up for grabs: https://github.com/mudler/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3A%22up+for+grabs%22

## 💻 [Getting started](https://localai.io/basics/getting_started/index.html)

For a detailed step-by-step introduction, refer to the [Getting Started](https://localai.io/basics/getting_started/index.html) guide. 

For those in a hurry, here's a straightforward one-liner to launch a LocalAI AIO(All-in-one) Image using `docker`:

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-aio-cpu
# or, if you have an Nvidia GPU:
# docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-aio-gpu-cuda12
```

## 🚀 [Features](https://localai.io/features/)

- 📖 [Text generation with GPTs](https://localai.io/features/text-generation/) (`llama.cpp`, `gpt4all.cpp`, ... [:book: and more](https://localai.io/model-compatibility/index.html#model-compatibility-table))
- 🗣 [Text to Audio](https://localai.io/features/text-to-audio/)
- 🔈 [Audio to Text](https://localai.io/features/audio-to-text/) (Audio transcription with `whisper.cpp`)
- 🎨 [Image generation with stable diffusion](https://localai.io/features/image-generation)
- 🔥 [OpenAI functions](https://localai.io/features/openai-functions/) 🆕
- 🧠 [Embeddings generation for vector databases](https://localai.io/features/embeddings/)
- ✍️ [Constrained grammars](https://localai.io/features/constrained_grammars/)
- 🖼️ [Download Models directly from Huggingface ](https://localai.io/models/)
- 🆕 [Vision API](https://localai.io/features/gpt-vision/)

## 💻 Usage

Check out the [Getting started](https://localai.io/basics/getting_started/index.html) section in our documentation.

### 🔗 Community and integrations

Build and deploy custom containers:
- https://github.com/sozercan/aikit

WebUIs:
- https://github.com/Jirubizu/localai-admin
- https://github.com/go-skynet/LocalAI-frontend

Model galleries
- https://github.com/go-skynet/model-gallery

Other:
- Helm chart https://github.com/go-skynet/helm-charts
- VSCode extension https://github.com/badgooooor/localai-vscode-plugin
- Local Smart assistant https://github.com/mudler/LocalAGI
- Home Assistant https://github.com/sammcj/homeassistant-localai / https://github.com/drndos/hass-openai-custom-conversation
- Discord bot https://github.com/mudler/LocalAGI/tree/main/examples/discord
- Slack bot https://github.com/mudler/LocalAGI/tree/main/examples/slack
- Telegram bot https://github.com/mudler/LocalAI/tree/master/examples/telegram-bot
- Examples: https://github.com/mudler/LocalAI/tree/master/examples/
  

### 🔗 Resources

- 🆕 New! [LLM finetuning guide](https://localai.io/docs/advanced/fine-tuning/)
- [How to build locally](https://localai.io/basics/build/index.html)
- [How to install in Kubernetes](https://localai.io/basics/getting_started/index.html#run-localai-in-kubernetes)
- [Projects integrating LocalAI](https://localai.io/docs/integrations/)
- [How tos section](https://io.midori-ai.xyz/howtos/) (curated by our community)

## :book: 🎥 [Media, Blogs, Social](https://localai.io/basics/news/#media-blogs-social)

- [Run LocalAI on AWS EKS with Pulumi](https://www.pulumi.com/ai/answers/tiZMDoZzZV6TLxgDXNBnFE/deploying-helm-charts-on-aws-eks)
- [Run LocalAI on AWS](https://staleks.hashnode.dev/installing-localai-on-aws-ec2-instance)
- [Create a slackbot for teams and OSS projects that answer to documentation](https://mudler.pm/posts/smart-slackbot-for-teams/)
- [LocalAI meets k8sgpt](https://www.youtube.com/watch?v=PKrDNuJ_dfE)
- [Question Answering on Documents locally with LangChain, LocalAI, Chroma, and GPT4All](https://mudler.pm/posts/localai-question-answering/)
- [Tutorial to use k8sgpt with LocalAI](https://medium.com/@tyler_97636/k8sgpt-localai-unlock-kubernetes-superpowers-for-free-584790de9b65)

## Citation

If you utilize this repository, data in a downstream project, please consider citing it with:

```
@misc{localai,
  author = {Ettore Di Giacinto},
  title = {LocalAI: The free, Open source OpenAI alternative},
  year = {2023},
  publisher = {GitHub},
  journal = {GitHub repository},
  howpublished = {\url{https://github.com/go-skynet/LocalAI}},
```

## ❤️ Sponsors

> Do you find LocalAI useful?

Support the project by becoming [a backer or sponsor](https://github.com/sponsors/mudler). Your logo will show up here with a link to your website.

A huge thank you to our generous sponsors who support this project:

| ![Spectro Cloud logo_600x600px_transparent bg](https://github.com/go-skynet/LocalAI/assets/2420543/68a6f3cb-8a65-4a4d-99b5-6417a8905512) |
|:-----------------------------------------------:|
|  [Spectro Cloud](https://www.spectrocloud.com/)  |
|  Spectro Cloud kindly supports LocalAI by providing GPU and computing resources to run tests on lamdalabs!  |

And a huge shout-out to individuals sponsoring the project by donating hardware or backing the project.

- [Sponsor list](https://github.com/sponsors/mudler)
- JDAM00 (donating HW for the CI)

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
