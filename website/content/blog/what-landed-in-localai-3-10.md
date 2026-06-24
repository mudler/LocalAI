---
title: "LocalAI 3.10: the Anthropic and Responses APIs, and one image for every GPU"
date: 2026-01-18
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "anthropic", "open-responses", "gpu", "moonshine"]
summary: "A /v1/messages endpoint that Claude clients can talk to unchanged, Open Responses compatibility that passes the official acceptance tests, and GPU libraries moved inside the backend containers so one image works on any hardware."
extracss: ["blog.css"]
---

Half the tooling worth using speaks a shape of API that is not OpenAI's. You find a client you like, it talks to Anthropic, and swapping it onto a local model means either rewriting the client or gluing a translation layer in front of it. Same story with the agent frameworks that went all in on the Responses API.

3.10.0 adds both surfaces natively, so the client does not have to know.

## Two more front doors

The Anthropic Messages API is served at `/v1/messages`, and at `/messages` for clients that do not prefix. Tool calling, streaming and non-streaming all work, so `anthropic-sdk-go`, LangChain and anything else built on that shape can be pointed at your instance without a code change.

The Open Responses API is at `/v1/responses`, with `/v1/responses/:id` to fetch one and `/v1/responses/:id/cancel` to stop it. It is stateful: pass a `response_id` and the conversation resumes, set `background: true` and the agent runs asynchronously while you go and do something else, then come back for the result. Streaming covers tools, images and audio.

That one passes the [official acceptance tests](https://www.openresponses.org/compliance), which was the bar I wanted to hit before shipping it.

## One image for every GPU

This is the change most likely to affect you even if you do not care about agents.

GPU libraries (CUDA, ROCm, Vulkan) now live inside the backend containers rather than in the image you pull. There is no longer a CUDA image, a ROCm image and a CPU image to choose between. You pull the image, and acceleration works if the hardware is there! Vulkan arm64 builds are in too.

It is experimental, and I want to be clear about that rather than bury it. It is a real architectural change to how every backend gets its libraries, and there will be hardware combinations we did not hit. If it does not work on yours, please file an issue, that is genuinely the most useful thing you can do for this one.

## Everything else

The backend gallery is system aware now, so it only lists backends your machine can actually run. No more scrolling past MLX entries on a Linux box.

Tool calls stream properly, including partial arguments as `input_json_delta`, and models that emit tools as XML (`<function>...</function>`) get parsed instead of dumping the markup into the message text. Both work across llama.cpp, vLLM and diffusers.

Thinking tags are extracted into a separate `reasoning` field rather than being left in the answer, in both SSE and non-SSE mode. The chat UI shows them under a Thinking tab.

There is a video generation page in the web UI with LTX-2 behind it, doing text-to-video and image-to-video with the usual `fps`, `num_frames` and `guidance_scale` controls.

There is request tracing now. `GET /api/traces` returns in-memory request and response logs, `/api/traces/clear` empties them. It is memory backed and drops old entries past a size cap, so it is for debugging an agent that is misbehaving right now, not for an audit trail.

Two new speech backends. Moonshine is an ONNX transcription engine aimed at low-end hardware, and it is the one to reach for on a Pi or an old laptop. It is quick! Pocket-TTS does lightweight TTS with voice cloning, though the cloning path needs a HuggingFace login and a registered voice model, so it is not quite copy-paste.

## Old hardware, and AMD memory

Two fixes worth calling out because they were silent failures rather than errors.

LocalAI was crashing on Intel CPUs without BMI2 (Sandy Bridge, Ivy Bridge), showing up as an `EOF` during model warmup rather than anything that pointed at the cause. It now falls back to `llama-cpp-fallback` on those chips.

On AMD, used and total VRAM were swapped when parsing `rocm-smi` output, so a dual-Radeon box reported nonsense. `HIP_VISIBLE_DEVICES` is also handled properly now, which matters if you are pinning to the discrete GPU.

## Thanks

Thanks to @richiejp, @majiayu000, @nanoandrew4, @DEVMANISHOFFL, @coffeerunhobby, @rampa3, @Nold360, @jroeber and @Divyanshupandey007 for the work in this cycle.

If the unified GPU backends misbehave on your setup, open an issue with what hardware you are on. And if you are wiring up the Anthropic or Responses endpoints and something does not match the spec, tell me, I would rather hear it from you than find out later.

[Full release notes](https://github.com/mudler/LocalAI/releases/tag/v3.10.0).
