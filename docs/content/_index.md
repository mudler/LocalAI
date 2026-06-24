+++
disableToc = false
title = "LocalAI documentation"
description = "Install LocalAI, run models, and operate it in production."
type = "home"
+++

LocalAI is the open source AI runtime: a small core that speaks the OpenAI and
Anthropic APIs, with each inference backend added only when a model needs it.
It runs text, vision, speech, sound, images, video, embeddings, reranking, and
autonomous agents on hardware you control, from a CPU laptop to a distributed
GPU cluster.

New here? Read the [Overview]({{% relref "overview" %}}) for what LocalAI is
and how the pieces fit together, then follow the
[Quickstart]({{% relref "getting-started/quickstart" %}}).

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest
```

## Sections

- **[Getting started]({{% relref "getting-started" %}})** - install LocalAI,
  run your first model, call the API, and fix the common startup problems.
- **[Features]({{% relref "features" %}})** - every capability, grouped by
  modality: text, agents, audio, vision, image and video, retrieval,
  distributed inference, and model management.
- **[Advanced]({{% relref "advanced" %}})** - model configuration, VRAM
  management, reverse proxies and TLS, and the rest of the fine-grained
  control surface.
- **[Operations]({{% relref "operations" %}})** - running and governing an
  instance: middleware, cloud and MITM proxies, backend monitoring.
- **[Reference]({{% relref "reference" %}})** - architecture, CLI flags, the
  compatibility table, API and runtime errors, system info, and binaries.
- **[FAQ]({{% relref "faq" %}})** - short answers to the questions that come up
  most often.

## Also useful

- **[Integrations]({{% relref "integrations" %}})** - projects and tools built
  on top of LocalAI.
- **[News]({{% relref "whats-new" %}})** - where release notes live.
- **[Model gallery](https://models.localai.io)** - browse the models you can
  install with one click.
- **[GitHub](https://github.com/mudler/LocalAI)** and
  **[Discord](https://discord.gg/uJAeKSAGDy)** - report an issue or ask a
  question.
