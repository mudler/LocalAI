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

<p align="center">
<a href="https://trendshift.io/repositories/1484" target="_blank"><img src="https://trendshift.io/api/badge/repositories/1484" alt="go-skynet%2FLocalAI | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/></a>
</p>

> :bulb: Get help - [❓FAQ](https://localai.io/faq/) [💭Discussions](https://github.com/go-skynet/LocalAI/discussions) [:speech_balloon: Discord](https://discord.gg/uJAeKSAGDy) [:book: Documentation website](https://localai.io/)
>
> [💻 Quickstart](https://localai.io/basics/getting_started/) [🖼️ Models](https://models.localai.io/) [🚀 Roadmap](https://github.com/mudler/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3Aroadmap) [🥽 Demo](https://demo.localai.io) [🌍 Explorer](https://explorer.localai.io) [🛫 Examples](https://github.com/mudler/LocalAI-examples) 

[![tests](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml)[![Build and Release](https://github.com/go-skynet/LocalAI/actions/workflows/release.yaml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/release.yaml)[![build container images](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml)[![Bump dependencies](https://github.com/go-skynet/LocalAI/actions/workflows/bump_deps.yaml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/bump_deps.yaml)[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/localai)](https://artifacthub.io/packages/search?repo=localai)

**LocalAI** is the free, Open Source OpenAI alternative. LocalAI act as a drop-in replacement REST API that’s compatible with OpenAI (Elevenlabs, Anthropic... ) API specifications for local AI inferencing. It allows you to run LLMs, generate images, audio (and not only) locally or on-prem with consumer grade hardware, supporting multiple model families. Does not require GPU. It is created and maintained by [Ettore Di Giacinto](https://github.com/mudler).

![screen](https://github.com/mudler/LocalAI/assets/2420543/20b5ccd2-8393-44f0-aaf6-87a23806381e)

Run the installer script:

```bash
curl https://localai.io/install.sh | sh
```

Or run with docker:
```bash
# CPU only image:
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-cpu

# Nvidia GPU:
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-gpu-nvidia-cuda-12

# CPU and GPU image (bigger size):
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest

# AIO images (it will pre-download a set of models ready for use, see https://localai.io/basics/container/)
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-aio-cpu
```

To load models:

```bash
# From the model gallery (see available models with `local-ai models list`, in the WebUI from the model tab, or visiting https://models.localai.io)
local-ai run llama-3.2-1b-instruct:q4_k_m
# Start LocalAI with the phi-2 model directly from huggingface
local-ai run huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf
# Install and run a model from the Ollama OCI registry
local-ai run ollama://gemma:2b
# Run a model from a configuration file
local-ai run https://gist.githubusercontent.com/.../phi-2.yaml
# Install and run a model from a standard OCI registry (e.g., Docker Hub)
local-ai run oci://localai/phi-2:latest
```

[💻 Getting started](https://localai.io/basics/getting_started/index.html)

## 📰 Latest project news

- Dec 2024: stablediffusion.cpp backend (ggml) added ( https://github.com/mudler/LocalAI/pull/4289 )
- Nov 2024: Bark.cpp backend added ( https://github.com/mudler/LocalAI/pull/4287 )
- Nov 2024: Voice activity detection models (**VAD**) added to the API: https://github.com/mudler/LocalAI/pull/4204
- Oct 2024: examples moved to [LocalAI-examples](https://github.com/mudler/LocalAI-examples)
- Aug 2024:  🆕 FLUX-1, [P2P Explorer](https://explorer.localai.io)
- July 2024: 🔥🔥 🆕 P2P Dashboard, LocalAI Federated mode and AI Swarms: https://github.com/mudler/LocalAI/pull/2723
- June 2024: 🆕 You can browse now the model gallery without LocalAI! Check out https://models.localai.io
- June 2024: Support for models from OCI registries: https://github.com/mudler/LocalAI/pull/2628
- May 2024: 🔥🔥 Decentralized P2P llama.cpp:  https://github.com/mudler/LocalAI/pull/2343 (peer2peer llama.cpp!) 👉 Docs  https://localai.io/features/distribute/
- May 2024: 🔥🔥 Openvoice: https://github.com/mudler/LocalAI/pull/2334
- May 2024: 🆕 Function calls without grammars and mixed mode: https://github.com/mudler/LocalAI/pull/2328
- May 2024: 🔥🔥 Distributed inferencing: https://github.com/mudler/LocalAI/pull/2324
- May 2024: Chat, TTS, and Image generation in the WebUI: https://github.com/mudler/LocalAI/pull/2222
- April 2024: Reranker API: https://github.com/mudler/LocalAI/pull/2121

Roadmap items: [List of issues](https://github.com/mudler/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3Aroadmap)

## 🔥🔥 Hot topics (looking for help):

- Multimodal with vLLM and Video understanding: https://github.com/mudler/LocalAI/pull/3729
- Realtime API https://github.com/mudler/LocalAI/issues/3714
- 🔥🔥 Distributed, P2P Global community pools: https://github.com/mudler/LocalAI/issues/3113
- WebUI improvements: https://github.com/mudler/LocalAI/issues/2156
- Backends v2: https://github.com/mudler/LocalAI/issues/1126
- Improving UX v2: https://github.com/mudler/LocalAI/issues/1373
- Assistant API: https://github.com/mudler/LocalAI/issues/1273
- Moderation endpoint: https://github.com/mudler/LocalAI/issues/999
- Vulkan: https://github.com/mudler/LocalAI/issues/1647
- Anthropic API: https://github.com/mudler/LocalAI/issues/1808

If you want to help and contribute, issues up for grabs: https://github.com/mudler/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3A%22up+for+grabs%22

## 🚀 [Features](https://localai.io/features/)

- 📖 [Text generation with GPTs](https://localai.io/features/text-generation/) (`llama.cpp`, `transformers`, `vllm` ... [:book: and more](https://localai.io/model-compatibility/index.html#model-compatibility-table))
- 🗣 [Text to Audio](https://localai.io/features/text-to-audio/)
- 🔈 [Audio to Text](https://localai.io/features/audio-to-text/) (Audio transcription with `whisper.cpp`)
- 🎨 [Image generation](https://localai.io/features/image-generation)
- 🔥 [OpenAI-alike tools API](https://localai.io/features/openai-functions/) 
- 🧠 [Embeddings generation for vector databases](https://localai.io/features/embeddings/)
- ✍️ [Constrained grammars](https://localai.io/features/constrained_grammars/)
- 🖼️ [Download Models directly from Huggingface ](https://localai.io/models/)
- 🥽 [Vision API](https://localai.io/features/gpt-vision/)
- 📈 [Reranker API](https://localai.io/features/reranker/)
- 🆕🖧 [P2P Inferencing](https://localai.io/features/distribute/)
- 🔊 Voice activity detection (Silero-VAD support)
- 🌍 Integrated WebUI!

## 💻 Usage

Check out the [Getting started](https://localai.io/basics/getting_started/index.html) section in our documentation.

### 🔗 Community and integrations

Build and deploy custom containers:
- https://github.com/sozercan/aikit

WebUIs:
- https://github.com/Jirubizu/localai-admin
- https://github.com/go-skynet/LocalAI-frontend
- QA-Pilot(An interactive chat project that leverages LocalAI LLMs for rapid understanding and navigation of GitHub code repository) https://github.com/reid41/QA-Pilot

Model galleries
- https://github.com/go-skynet/model-gallery

Other:
- Helm chart https://github.com/go-skynet/helm-charts
- VSCode extension https://github.com/badgooooor/localai-vscode-plugin
- Langchain: https://python.langchain.com/docs/integrations/providers/localai/
- Terminal utility https://github.com/djcopley/ShellOracle
- Local Smart assistant https://github.com/mudler/LocalAGI
- Home Assistant https://github.com/sammcj/homeassistant-localai / https://github.com/drndos/hass-openai-custom-conversation / https://github.com/valentinfrlch/ha-gpt4vision
- Discord bot https://github.com/mudler/LocalAGI/tree/main/examples/discord
- Slack bot https://github.com/mudler/LocalAGI/tree/main/examples/slack
- Shell-Pilot(Interact with LLM using LocalAI models via pure shell scripts on your Linux or MacOS system) https://github.com/reid41/shell-pilot
- Telegram bot https://github.com/mudler/LocalAI/tree/master/examples/telegram-bot
- Another Telegram Bot https://github.com/JackBekket/Hellper
- Auto-documentation https://github.com/JackBekket/Reflexia
- Github bot which answer on issues, with code and documentation as context https://github.com/JackBekket/GitHelper
- Github Actions: https://github.com/marketplace/actions/start-localai
- Examples: https://github.com/mudler/LocalAI/tree/master/examples/
  

### 🔗 Resources

- [LLM finetuning guide](https://localai.io/docs/advanced/fine-tuning/)
- [How to build locally](https://localai.io/basics/build/index.html)
- [How to install in Kubernetes](https://localai.io/basics/getting_started/index.html#run-localai-in-kubernetes)
- [Projects integrating LocalAI](https://localai.io/docs/integrations/)
- [How tos section](https://io.midori-ai.xyz/howtos/) (curated by our community)

## :book: 🎥 [Media, Blogs, Social](https://localai.io/basics/news/#media-blogs-social)

- [Run Visual studio code with LocalAI (SUSE)](https://www.suse.com/c/running-ai-locally/)
- 🆕 [Run LocalAI on Jetson Nano Devkit](https://mudler.pm/posts/local-ai-jetson-nano-devkit/)
- [Run LocalAI on AWS EKS with Pulumi](https://www.pulumi.com/blog/low-code-llm-apps-with-local-ai-flowise-and-pulumi/)
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

A huge thank you to our generous sponsors who support this project covering CI expenses, and our [Sponsor list](https://github.com/sponsors/mudler):

<p align="center">
  <a href="https://www.spectrocloud.com/" target="blank">
    <img height="200" src="https://github.com/go-skynet/LocalAI/assets/2420543/68a6f3cb-8a65-4a4d-99b5-6417a8905512">
  </a>
  <a href="https://www.premai.io/" target="blank">
    <img height="200" src="https://github.com/mudler/LocalAI/assets/2420543/42e4ca83-661e-4f79-8e46-ae43689683d6"> <br>
  </a>
</p>

## 🌟 Star history

[![LocalAI Star history Chart](https://api.star-history.com/svg?repos=go-skynet/LocalAI&type=Date)](https://star-history.com/#go-skynet/LocalAI&Date)

## 📖 License

LocalAI is a community-driven project created by [Ettore Di Giacinto](https://github.com/mudler/).

MIT - Author Ettore Di Giacinto <mudler@localai.io>

## 🙇 Acknowledgements

LocalAI couldn't have been built without the help of great software already available from the community. Thank you!

- [llama.cpp](https://github.com/ggerganov/llama.cpp)
- https://github.com/tatsu-lab/stanford_alpaca
- https://github.com/cornelk/llama-go for the initial ideas
- https://github.com/antimatter15/alpaca.cpp
- https://github.com/EdVince/Stable-Diffusion-NCNN
- https://github.com/ggerganov/whisper.cpp
- https://github.com/rhasspy/piper

## 🤗 Contributors

This is a community project, a special thanks to our contributors! 🤗
<a href="https://github.com/go-skynet/LocalAI/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=go-skynet/LocalAI" />
</a>
