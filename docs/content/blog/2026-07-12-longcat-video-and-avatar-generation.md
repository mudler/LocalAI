+++
title = "LongCat video and avatar generation"
date = 2026-07-12
description = "A dedicated CUDA backend for LongCat-Video text/image-to-video and LongCat-Video-Avatar-1.5 speech-driven avatars."
url = "/blog/longcat-video-and-avatar-generation/"
+++

LocalAI gains a dedicated CUDA backend for the LongCat family: `LongCat-Video` for text-to-video and image-to-video, and `LongCat-Video-Avatar-1.5` for speech-driven avatars.

Highlights:

- Multi-segment continuation, so a clip can be extended beyond a single generation window.
- Portrait and recorded-audio inputs wired into Studio.
- An SDPA CUDA 13 ARM64 build, which makes the backend usable on DGX Spark.

See [Video generation]({{% relref "features/video-generation" %}}) for configuration and the available model entries. Shipped in [PR #10792](https://github.com/mudler/LocalAI/pull/10792).
