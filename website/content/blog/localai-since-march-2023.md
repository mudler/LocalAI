---
title: "LocalAI, from March 2023 to now"
date: 2026-07-29
author: "Ettore Di Giacinto"
category: "History"
tags: ["history", "architecture", "releases", "community"]
summary: "Three years, 133 releases and 224 contributors later. Here are the four decisions that shaped it: making the core small, adding agents, making it a cluster, and giving it eyes and ears."
extracss: ["blog.css"]
---

I created the LocalAI repository on 18 March 2023. It was an OpenAI-compatible HTTP API in front of llama.cpp, and that was the whole idea: change one URL in code you already wrote, and the answers start coming from your own laptop instead of somebody's datacenter.

Three years on, 224 people have put code into it, across 133 releases. It has picked up 48,042 stars along the way, which still surprises me. It now runs 73 different backends, you can install any of 1,585 models from the gallery without writing a line of Python, and eighteen of the engines doing the actual work are C or C++ ports we sat down and wrote ourselves.

None of those numbers are rounded up. You can read every one of them off the repository or the GitHub API right now, and the ones further down this post come from the release notes.

{{< starchart >}}

The four marks on it are the four decisions below, and you can see each of them in the slope afterwards.

What follows is the four decisions that changed the shape of the thing. The full feature list is in the releases.

## 2023 to 2024: an API in front of llama.cpp

One rule has not moved since the first commit: if a feature only works on a GPU, it does not ship as the main path. Every modality gets a CPU path, and that path is tested. Most people do not have a spare A100 sitting around, and the ones who do still want to develop on the train.

The first two years were spent widening what sat behind the API while keeping that constraint: whisper.cpp for transcription, stable-diffusion for images, embeddings, then reranking, then constrained grammars and the function-calling surface. The API compatibility list grew alongside it, and later picked up the Anthropic and ElevenLabs shapes as well as OpenAI's, so that the same server answers whichever client somebody already has.

The bill for all that breadth came due in the binary. Everything was compiled in, so you downloaded CUDA kernels whether or not you owned a GPU, a Python runtime whether or not you wanted one, and image models when all you asked for was chat. Building from source meant building the lot. It got embarrassing.

## July 2025: the core gets small

Every backend moved out of the main binary in [v3.2.0](https://github.com/mudler/LocalAI/releases/tag/v3.2.0). Backends became separate OCI images, pulled on demand the first time a model asks for one, with a small core that speaks the API and manages processes.

You install one thing and it stays small. Ask for a GGUF model and llama-cpp arrives. Ask for transcription and whisper or parakeet arrives. Nothing else is fetched, and a machine that only ever serves one model never downloads the other sixty-nine backends.

Everything after it depended on that one change. Adding a backend stopped meaning adding weight to everybody's install, so "should we support this engine" stopped being an argument about download size and went back to being an argument about whether the engine is any good. It is also the reason we can afford to maintain eighteen engines of our own, which comes later.

## March 2026: agents, and a new interface

[LocalAI 4.0.0](https://github.com/mudler/LocalAI/releases/tag/v4.0.0) added native agentic orchestration with the [Agenthub](https://agenthub.localai.io) community hub, so agents with tool use, RAG, skills and streaming run inside the same server rather than as a separate stack you wire up yourself.

The web interface was rewritten in React at the same time, with a Canvas mode, MCP Apps and client-side tools with tool streaming ([#8947](https://github.com/mudler/LocalAI/pull/8947)), and WebRTC realtime audio ([#8790](https://github.com/mudler/LocalAI/pull/8790)). MLX gained a distributed mode ([#8801](https://github.com/mudler/LocalAI/pull/8801)).

The realtime audio path changed what people built with it. Speech in, tool calls in the middle, speech out, over WebRTC, fast enough that it feels like a conversation rather than a walkie-talkie. It had landed as the Realtime API in February 2026 ([#6245](https://github.com/mudler/LocalAI/pull/6245)), and the interface rewrite finally gave it a face.

## April 2026: it becomes a cluster

[LocalAI 4.1.0](https://github.com/mudler/LocalAI/releases/tag/v4.1.0) added distributed cluster mode. You start a worker on another box, it reports its hardware and the backends it can run, and it joins the pool. Requests are placed by a scheduler that knows real free VRAM rather than a static guess, and the pool autoscales.

The same release turned LocalAI into something you can put more than one person on: OIDC and API keys, per-user quotas with predictive analytics, in-UI fine-tuning with TRL that exports straight to GGUF, an on-the-fly quantization backend, and a visual pipeline editor.

[4.3.0](https://github.com/mudler/LocalAI/releases/tag/v4.3.0) in May followed with per-request replica routing, per-API-key and per-user usage attribution ([#9920](https://github.com/mudler/LocalAI/pull/9920)), keyless cosign signing of the backend OCI images ([#9823](https://github.com/mudler/LocalAI/pull/9823)), and llama.cpp prompt caching on by default ([#9925](https://github.com/mudler/LocalAI/pull/9925)), which collapses repeated system prompts from minutes to seconds. In June, prefix-cache-aware routing ([#10071](https://github.com/mudler/LocalAI/pull/10071)) started sending a request to the replica that already holds the matching prefix cache rather than to whichever node was least busy.

Distributed mode is also where most of the painful bugs have lived since. In the 4.8.0 cycle alone we fixed a reaper that cheerfully deleted rows for backends that were alive and busy, frontend stubs that outlived the replicas behind them, and `in_flight` counters that leaked and then pinned VRAM against eviction. Running across machines finds failure modes a single process will never show you, and it finds them in production.

## May and June 2026: it sees and hears

[LocalAI 4.2.0](https://github.com/mudler/LocalAI/releases/tag/v4.2.0) added voice recognition ([#9500](https://github.com/mudler/LocalAI/pull/9500)), face recognition with anti-spoofing liveness ([#9480](https://github.com/mudler/LocalAI/pull/9480)) and speaker diarization, alongside video generation ([#9420](https://github.com/mudler/LocalAI/pull/9420)), a drop-in Ollama API ([#9284](https://github.com/mudler/LocalAI/pull/9284)) and eleven new backends.

A chat model only knows what somebody typed at it. Recognition widens that: who is in the room, who is talking, whether the face at the camera is a live person or a photo somebody is holding up. All of it runs on the same machine as the model, which for biometrics is the only arrangement most people can honestly deploy at all.

In June those two capabilities stopped being Python. [voice-detect.cpp](https://github.com/localai-org/voice-detect.cpp) and [face-detect.cpp](https://github.com/mudler/face-detect.cpp) replaced the Python `speaker-recognition` and `insightface` backends with from-scratch C++ and ggml engines, self-contained GGUF weights, no onnxruntime at inference, and bit-exact parity with the references they replaced ([#10441](https://github.com/mudler/LocalAI/pull/10441)).

## Mid 2026: eighteen engines of our own

The README now has a table called "Backends built by us". It lists eighteen native C and C++ engines, plus [apex-quant](https://github.com/localai-org/apex-quant), our quantization recipe for mixture-of-experts models.

Each one exists for one of three reasons. Some replace a Python dependency that was simply too heavy to ship, like insightface, speaker-recognition, or vLLM itself. Some port a model nobody had written in C++ yet: Depth Anything 3, LocateAnything, CED audio tagging, TRELLIS.2. And some fill a hole in what a local assistant can do at all, like acoustic echo cancellation, which is the unglamorous thing that stops a voice loop from sitting there transcribing its own speaker.

The recipe is the same every time. A model shows up as PyTorch or ONNX plus a Python package. We port the graph to ggml, convert the weights into one GGUF file, and gate every component against reference tensors dumped from the original. Only after it is correct do we let ourselves look at the clock. LocalAI then loads the shared library through purego over a flat C ABI, so there is no Python process anywhere on the serving path.

The most recent one is [vllm.cpp](https://github.com/mudler/vllm.cpp), a C++20 port of vLLM's V1 serving architecture with paged KV cache, continuous batching and prefix caching, which shipped as the `vllm-cpp` backend in 4.8.0.

## Where it stands

Still MIT, still a community project. 224 people have put code in, and the README is kept translated into eight languages because the people using this are not all in one place. The [contributors graph](https://github.com/mudler/LocalAI/graphs/contributors) shows who actually built this, and it is not me.

If you want to add something, backends and gallery entries are the two places a first contribution lands cleanly. There is a step-by-step checklist for a new backend in `.agents/adding-backends.md`, and a gallery entry is just a YAML block. Come say hello in [Discord](https://discord.gg/uJAeKSAGDy) if you get stuck.
