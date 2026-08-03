+++
title = "Face recognition backend"
date = 2026-04-22
description = "insightface-powered 1:1 verification, 1:N identification, face embedding, detection and demographic analysis."
url = "/blog/face-recognition-backend/"
+++

A new face recognition backend, powered by `insightface`, covering:

- 1:1 verification
- 1:N identification
- Face embedding
- Face detection
- Demographic analysis

It ships with two model options: the non-commercial `buffalo_l`, and an Apache 2.0 alternative from the OpenCV Zoo.

See [Face recognition]({{% relref "features/face-recognition" %}}). Shipped in [PR #9480](https://github.com/mudler/LocalAI/pull/9480).

The engine was later rewritten from scratch in C++/ggml: see [Native biometric backends]({{% relref "blog/2026-06-28-native-biometric-backends" %}}).
