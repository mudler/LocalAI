+++
disableToc = false
title = "LocalAI documentation"
description = "Install LocalAI, run models, and operate it."
type = "home"
+++

LocalAI is an open source AI runtime. A small core speaks the OpenAI and
Anthropic APIs, and each inference backend is added only when a model needs it.
It runs text, vision, speech, sound, image, video, embedding and reranking
models, and agents, on hardware you choose, from a laptop CPU to a GPU cluster.

New here? Read the [overview]({{% relref "/start/overview" %}}), then follow the
[quickstart]({{% relref "/start/quickstart" %}}).

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest
```

## Sections

- **[Start here]({{% relref "/start" %}})**: install LocalAI, run a first model, call the API and build a first agent.
- **[Concepts]({{% relref "/concepts" %}})**: how the parts fit together, and how users and sharing work.
- **[Features]({{% relref "/features" %}})**: every capability, grouped by task.
- **[Add-ons]({{% relref "/addons" %}})**: install add-ons, use them, and build your own.
- **[Models and backends]({{% relref "/models" %}})**: the model gallery, model configuration, backends and GPU support.
- **[API]({{% relref "/api" %}})**: the HTTP API, tools, realtime and error codes.
- **[Administration]({{% relref "/administration" %}})**: users, settings, monitoring, proxies, distributed mode and troubleshooting.
- **[Reference]({{% relref "/reference" %}})**: command line, binaries, system information and the FAQ.
- **[Contributing]({{% relref "/contributing" %}})**: build from source, test add-ons and write docs.

## Also useful

- **[Integrations]({{% relref "/api/integrations" %}})**: projects and tools built on LocalAI.
- **[News]({{% relref "/start/whats-new" %}})**: where release notes live.
- **[Model gallery](https://models.localai.io)**: browse the models you can install.
- **[GitHub](https://github.com/mudler/LocalAI)** and **[Discord](https://discord.gg/uJAeKSAGDy)**: report an issue or ask a question.
