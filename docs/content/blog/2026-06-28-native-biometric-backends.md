+++
title = "Native biometric backends: voice-detect.cpp and face-detect.cpp"
date = 2026-06-28
description = "Two from-scratch C++/ggml engines replace the heavier Python insightface and speaker-recognition backends."
url = "/blog/native-biometric-backends/"
+++

Two new biometric engines built by the LocalAI team, both from-scratch C++/ggml implementations with no Python and no onnxruntime at inference time:

- [voice-detect.cpp](https://github.com/localai-org/voice-detect.cpp) for speaker recognition and voice analysis: ECAPA-TDNN, WeSpeaker, ERes2Net, CAM++, and wav2vec2 age/gender/emotion.
- [face-detect.cpp](https://github.com/mudler/face-detect.cpp) for face detection, recognition, demographics and anti-spoofing: SCRFD/ArcFace and YuNet/SFace.

Both ship self-contained GGUF weights, hold bit-exact parity with the reference implementations, and reach cuDNN parity on GPU. They replace the heavier Python `insightface` and `speaker-recognition` backends.

Shipped in [PR #10441](https://github.com/mudler/LocalAI/pull/10441).
