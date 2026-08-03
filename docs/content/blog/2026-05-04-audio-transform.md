+++
title = "Audio Transform"
date = 2026-05-04
description = "A generic audio-in / audio-out endpoint with an optional reference signal. First implementation: LocalVQE, a joint AEC, noise suppression and dereverberation engine."
url = "/blog/audio-transform/"
+++

Audio Transform is a generic audio-in / audio-out endpoint, with an optional reference signal for tasks that need one.

The first implementation is [LocalVQE](https://github.com/localai-org/LocalVQE), a C++ backend doing joint acoustic echo cancellation, noise suppression and dereverberation in a DeepVQE-style model.

Both call styles are supported:

- Batch, via `POST /audio/transformations`.
- Bidirectional streaming, via the `/audio/transformations/stream` WebSocket.

Studio gains a "Transform" tab with synchronized waveform players for the input, reference and output signals.

See [Audio transform]({{% relref "features/audio-transform" %}}). Shipped in [PR #9640](https://github.com/mudler/LocalAI/pull/9640).
