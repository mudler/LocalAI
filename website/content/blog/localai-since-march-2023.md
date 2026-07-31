---
title: "LocalAI, from March 2023 to now"
date: 2026-07-29
author: "Ettore Di Giacinto"
category: "History"
tags: ["history", "architecture", "releases", "community"]
summary: "Three years, 133 releases and 224 contributors later. The four changes that mattered most were making the core small, adding agents, making it a cluster, and giving it eyes and ears."
extracss: ["blog.css"]
---

The LocalAI repository was created on 18 March 2023. At the time it was an OpenAI-compatible HTTP API in front of llama.cpp, so that a laptop could answer the same calls as the cloud and existing client code would keep working after a URL change.

Today the same repository has 48,042 stars, 4,314 forks, 224 contributors and 133 releases. `backend/index.yaml` lists 73 backend families, `gallery/index.yaml` has 1,585 entries, and eighteen of the engines behind those backends are C or C++ ports we wrote and maintain ourselves. Those figures are all readable off the repository and the GitHub API today, and the ones in the middle of this post come from the release notes.

What follows is the short version of how it got here, organized around the four changes that altered the shape of the project rather than the feature list.

## 2023 to 2024: an API in front of llama.cpp

The founding constraint has not moved since the first commit. If a feature only works on a GPU, it does not ship as the primary path. Every modality has a CPU path that is tested, because most people do not have a spare A100 and the ones who do still want to develop on a laptop.

The first two years were spent widening what sat behind the API while keeping that constraint: whisper.cpp for transcription, stable-diffusion for images, embeddings, then reranking, then constrained grammars and the function-calling surface. The API compatibility list grew alongside it, and later picked up the Anthropic and ElevenLabs shapes as well as OpenAI's, so that the same server answers whichever client somebody already has.

The cost of that breadth was the binary. Every backend was compiled in, so the download carried CUDA kernels for people who had no GPU, Python runtimes for people who only wanted GGUF, and image models for people who only wanted chat. Building from source meant building all of it.

## July 2025: the core gets small

Every backend moved out of the main binary in [v3.2.0](https://github.com/mudler/LocalAI/releases/tag/v3.2.0). Backends became separate OCI images, pulled on demand the first time a model asks for one, with a small core that speaks the API and manages processes.

You install one thing and it stays small. Ask for a GGUF model and llama-cpp arrives. Ask for transcription and whisper or parakeet arrives. Nothing else is fetched, and a machine that only ever serves one model never downloads the other sixty-nine backends.

That change is what made everything after it possible. Adding a backend stopped meaning adding weight to every installation, so the answer to "should we support this engine" stopped being a tradeoff against download size. It is also why the project can maintain its own engines at all, which is a later part of this story.

## March 2026: agents, and a new interface

[LocalAI 4.0.0](https://github.com/mudler/LocalAI/releases/tag/v4.0.0) added native agentic orchestration with the [Agenthub](https://agenthub.localai.io) community hub, so agents with tool use, RAG, skills and streaming run inside the same server rather than as a separate stack you wire up yourself.

The web interface was rewritten in React at the same time, with a Canvas mode, MCP Apps and client-side tools with tool streaming ([#8947](https://github.com/mudler/LocalAI/pull/8947)), and WebRTC realtime audio ([#8790](https://github.com/mudler/LocalAI/pull/8790)). MLX gained a distributed mode ([#8801](https://github.com/mudler/LocalAI/pull/8801)).

The realtime audio path is the piece that changed what people built with it. Speech in, tool calls in the middle, speech out, over WebRTC, at a latency that holds a conversation together. That had landed as the Realtime API in February 2026 ([#6245](https://github.com/mudler/LocalAI/pull/6245)) and the UI rewrite gave it a front end.

## April 2026: it becomes a cluster

[LocalAI 4.1.0](https://github.com/mudler/LocalAI/releases/tag/v4.1.0) added distributed cluster mode. You start a worker on another box, it reports its hardware and the backends it can run, and it joins the pool. Requests are placed by a scheduler that knows real free VRAM rather than a static guess, and the pool autoscales.

The same release turned LocalAI into something you can put more than one person on: OIDC and API keys, per-user quotas with predictive analytics, in-UI fine-tuning with TRL that exports straight to GGUF, an on-the-fly quantization backend, and a visual pipeline editor.

[4.3.0](https://github.com/mudler/LocalAI/releases/tag/v4.3.0) in May followed with per-request replica routing, per-API-key and per-user usage attribution ([#9920](https://github.com/mudler/LocalAI/pull/9920)), keyless cosign signing of the backend OCI images ([#9823](https://github.com/mudler/LocalAI/pull/9823)), and llama.cpp prompt caching on by default ([#9925](https://github.com/mudler/LocalAI/pull/9925)), which collapses repeated system prompts from minutes to seconds. In June, prefix-cache-aware routing ([#10071](https://github.com/mudler/LocalAI/pull/10071)) started sending a request to the replica that already holds the matching prefix cache rather than to whichever node was least busy.

Distributed mode is also where most of the hard bugs since have been. The 4.8.0 cycle alone fixed a reaper that deleted rows for backends that were alive and busy, frontend stubs that outlived their replicas, and `in_flight` counters that leaked and pinned VRAM against eviction. Running something across machines surfaces failure modes that a single process never shows you.

## May and June 2026: it sees and hears

[LocalAI 4.2.0](https://github.com/mudler/LocalAI/releases/tag/v4.2.0) added voice recognition ([#9500](https://github.com/mudler/LocalAI/pull/9500)), face recognition with anti-spoofing liveness ([#9480](https://github.com/mudler/LocalAI/pull/9480)) and speaker diarization, alongside video generation ([#9420](https://github.com/mudler/LocalAI/pull/9420)), a drop-in Ollama API ([#9284](https://github.com/mudler/LocalAI/pull/9284)) and eleven new backends.

A chat model can only work with what somebody typed at it. Recognition changes the input surface: who is in the room, who is speaking, whether the face in front of the camera is a live person or a photograph held up to it. All of it runs on the same machine as the model, which for biometrics is the only arrangement most people can actually deploy.

In June those two capabilities stopped being Python. [voice-detect.cpp](https://github.com/localai-org/voice-detect.cpp) and [face-detect.cpp](https://github.com/mudler/face-detect.cpp) replaced the Python `speaker-recognition` and `insightface` backends with from-scratch C++ and ggml engines, self-contained GGUF weights, no onnxruntime at inference, and bit-exact parity with the references they replaced ([#10441](https://github.com/mudler/LocalAI/pull/10441)).

## Mid 2026: eighteen engines of our own

The README now has a table called "Backends built by us". It lists eighteen native C and C++ engines, plus [apex-quant](https://github.com/localai-org/apex-quant), our quantization recipe for mixture-of-experts models.

They exist for three reasons, one per engine roughly. Some replace a Python dependency that was too heavy to ship (insightface, speaker-recognition, vLLM itself). Some port a model that had no C++ implementation at all (Depth Anything 3, LocateAnything, CED audio tagging, TRELLIS.2). Some fill a gap in what a local assistant can do, such as acoustic echo cancellation, which is what stops a voice loop from transcribing its own speaker output.

The pattern behind all of them is the same. A model is published as PyTorch or ONNX plus a Python package. We port the graph to ggml, convert the weights to a single GGUF, gate every component against reference tensors dumped from the original, and only then look at speed. LocalAI loads the resulting shared library through purego and calls a flat C ABI, so there is no Python process anywhere on the serving path.

The most recent one is [vllm.cpp](https://github.com/mudler/vllm.cpp), a C++20 port of vLLM's V1 serving architecture with paged KV cache, continuous batching and prefix caching, which shipped as the `vllm-cpp` backend in 4.8.0.

## Where it stands

LocalAI is still MIT licensed and still a community project. 224 people have contributed code, the README is kept translated into eight languages because the users are not all in one place, and the [contributors graph](https://github.com/mudler/LocalAI/graphs/contributors) is the honest picture of who built this.

If you want to add something, backends and gallery entries are the two places where a first contribution lands cleanly. The step-by-step checklist for a new backend is in `.agents/adding-backends.md` in the repository, and gallery entries are a YAML file.
