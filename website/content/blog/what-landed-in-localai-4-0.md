---
title: "LocalAI 4.0: agents in the core, and a React interface"
date: 2026-03-14
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "agents", "agenthub", "mcp", "react", "webrtc"]
summary: "Native agent orchestration with the Agenthub, a rewritten interface with Canvas mode, MCP Apps with tool streaming, and two things removed."
extracss: ["blog.css"]
---

Running an agent locally has meant running two things: an inference server, and a separate orchestrator that talks to it. That is a lot of moving parts for something you wanted to try on a Tuesday evening.

4.0.0 puts the agent side in the core. You create agents, give them memory and skills, connect them to MCP servers, and start and stop them from the same interface you already use for models.

This is a major version bump, so there are two removals near the bottom of this post. Read those before you upgrade.

## Agents, and the Agenthub

Agents are managed through the React interface: create one, wire up MCP servers and skills, connect it to Slack, watch what it is doing through a new Events column in the agents list.

Memory has two options. Hybrid search backed by PostgreSQL if you already run one, or in-memory storage via Chromem if you do not want another service. Skills live in a central database rather than being pasted per agent.

The bit I am most curious to see used is [Agenthub](https://agenthub.localai.io), a community space for sharing agent configurations. You publish one, somebody else imports it into their instance and runs it against their own models on their own hardware!

## The interface is React now

The web interface has been rewritten. The old one had reached the point where adding anything meant fighting it.

Canvas mode is the new thing worth turning on: enable it in chat and code blocks and artifacts the model produces render in a preview pane on the right instead of scrolling past you as text. The System view splits Models and Backends into tabs. Traces render as accordions, which makes a long one readable. And if you try to install a model whose weights exceed your system RAM, you get a warning first rather than a locked-up machine.

## MCP Apps

Client-side MCP support is complete in this release ([#8947](https://github.com/mudler/LocalAI/pull/8947)). You pick which MCP servers to enable for a chat directly in the interface, and their tools get injected into the normal chat with streaming, so there is no separate agent mode to switch into.

If you would rather not have any of it, `LOCALAI_DISABLE_MCP` turns the whole thing off.

## Audio, video, and MLX across machines

WebRTC is wired into the Realtime API and the Talk page ([#8790](https://github.com/mudler/LocalAI/pull/8790)), which is a real improvement for latency over what was there before.

Three new audio backends: fish-speech, ace-step.cpp, and faster-qwen3-tts (CUDA only). TTS gained `sample_rate` support through post-processing, and Qwen TTS handles multiple voices.

There is also an experimental MLX distributed backend for spreading a workload across Apple machines ([#8801](https://github.com/mudler/LocalAI/pull/8801)). It is early, so expect rough edges if you try it.

## Infrastructure

Persistent data now has its own location, separate from configuration. `LOCALAI_DATA_PATH` (or `--data-path`) points at where agents, skills, tasks, jobs and the collection database live, defaulting to `data/` under the base path. If you are mounting volumes, this is the one to look at.

Shell completion scripts generate for bash, zsh and fish. There is dedicated Podman documentation now, including rootless setup.

## Two things are gone

The HuggingFace backend has been removed.

AIO images are dropped. They existed to bundle a preset of models with the runtime, and maintaining them across every hardware variant stopped being worth what they gave people. Use the main images and install models from the gallery.

## One known issue

The `diffusers` backend is not in this release. It failed to build because we exhausted our CI limits, so the previous version is still what you get if you install it.

This is an infrastructure problem, not a code one, and it is the kind of thing that will keep happening to us. If you know anybody at GitHub who could help us get better ARM runners, please reach out, I am not too proud to ask.

## Thanks

Thanks to @richiejp, @nanoandrew4, @Weathercold, @sozercan, @lukasdotcom, @loryanstrant, @bittoby and @attilagyorffy.

If you build an agent worth sharing, put it on the Agenthub. The more the merrier!

[Full release notes](https://github.com/mudler/LocalAI/releases/tag/v4.0.0).
