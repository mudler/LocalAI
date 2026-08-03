+++
title = "Dynamic memory reclaimer, multi-GPU fitting and Vibevoice"
date = 2025-12-16
description = "Reclaim GPU memory from idle models, fit llama.cpp models across multiple GPUs automatically, and generate long-form speech with Vibevoice."
url = "/blog/memory-reclaimer-and-multi-gpu-fitting/"
+++

Three additions this month:

- [A dynamic memory resource reclaimer](https://github.com/mudler/LocalAI/pull/7583), which frees GPU memory held by idle models.
- [Automatic multi-GPU model fitting for llama.cpp](https://github.com/mudler/LocalAI/pull/7584), so a model too large for one device is split across several without hand-tuning.
- [The Vibevoice backend](https://github.com/mudler/LocalAI/pull/7494) for long-form speech.
