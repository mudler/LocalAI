---
title: "LocalAI 4.1: more than one box, and more than one user"
date: 2026-04-02
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "distributed", "auth", "oidc", "quotas", "fine-tuning"]
summary: "Distributed cluster mode that places requests by real free VRAM, OIDC with per-user API keys and quotas, and LoRA fine-tuning that exports straight to GGUF."
extracss: ["blog.css"]
---

Two problems show up the moment LocalAI stops being a thing you run for yourself.

The first is that you have more than one machine, and only one of them is doing any work. The second is that other people are using your instance, and you have no way to tell who is burning the GPU, or to stop them.

4.1.0 is mostly about those two.

## Running as a cluster

Distributed mode lets you point several nodes at one control plane and stop thinking about which one to call.

Routing orders nodes by available VRAM, so the request lands on the card with room for it. Node groups let you pin models to a subset of the cluster, which is how you keep a heavy diffusion model off the boxes doing embeddings. There is a min/max autoscaler with a reconciler managing node lifecycle, and you can drain a node for maintenance and resume it later through the API instead of pulling it out from under in-flight requests.

Model transfer between nodes goes over S3 or peer to peer, so a model you have already pulled once does not have to come down from the internet again on every node!

The cluster status shows up on the home page.

## Users, keys and quotas

LocalAI ships a multi-user platform now, which is the piece that makes it deployable for a team or a classroom rather than just for you.

- User management from the React interface.
- OIDC/OAuth against your own identity provider (Google, Keycloak, Authentik, whatever you already run).
- Invite mode, so registration is closed unless an admin lets somebody in.
- Per-user API keys.
- Admin impersonation, for when somebody reports a bug you cannot reproduce.

On top of that there is a quota system: set per-user limits and have them enforced, with a usage dashboard broken down per user and a predictive view of where consumption is heading.

## Fine-tuning without leaving the interface

Both of these are experimental. I would use them on something you can afford to throw away.

Fine-tuning uses HuggingFace TRL to train LoRA adapters, exports the result to GGUF automatically, and imports it back into LocalAI so you can serve what you just trained without moving files around by hand. There is a small evals framework included to check whether the thing you trained is actually better.

The quantization backend produces optimized variants of a model on the fly.

## Agents from the terminal

You can run an agent without the server now:

```sh
local-ai agent run <name>
local-ai agent list
```

`run` takes an agent from the pool registry in `pool.json`, or a single-turn `--prompt` if you just want one answer. Tool calls stream in real time, and the interleaved-thinking bug that mangled output when a model reasoned mid-tool-call is fixed.

## The rest of the interface work

The model pipeline editor is visual, so wiring models together no longer means editing YAML. Backend logs can be scoped to a single model rather than reading the whole stream. Studio pages remember past generations, so images and audio you made last week are still there. The model and backend selectors are searchable. Error toasts link straight to the trace that produced them.

## Under the hood

Inference defaults are pulled from Unsloth and applied across all endpoints and gallery models, so models arrive with sane sampling parameters instead of whatever the default happened to be. `min_p` is supported. When native tool-call parsing fails, an iterative fallback parser takes over rather than returning nothing.

Repeated log lines get collapsed. NVIDIA Jetson and Tegra are detected as first-class platforms. SYCL backends auto-disable `mmap`, which was crashing them on Intel GPUs. llama.cpp bundles `libdl`, `librt` and `libpthread` for portability. And the downloader rewrites HuggingFace URIs through `HF_ENDPOINT`, which is the one you need if you are behind a corporate mirror.

## Thanks

Thanks to @richiejp for a large chunk of this cycle, and to @tv42, @walcz-de, @majiayu000 and @ER-EPR.

There is a full setup walkthrough on video if you would rather watch than read: [youtube.com/watch?v=cMVNnlqwfw4](https://www.youtube.com/watch?v=cMVNnlqwfw4).

If you are setting up distributed mode or OIDC and hit a wall, reach out, I am happy to help you get it standing up.

[Full release notes](https://github.com/mudler/LocalAI/releases/tag/v4.1.0).
