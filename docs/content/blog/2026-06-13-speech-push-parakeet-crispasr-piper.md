+++
title = "A big speech push: parakeet.cpp, CrispASR and 60 Piper voices"
date = 2026-06-13
description = "Segment timestamps, multilingual streaming, dynamic batching and CUDA graphs for parakeet.cpp, plus a new ASR/TTS backend and a large Piper voice drop."
url = "/blog/speech-push-parakeet-crispasr-piper/"
+++

A concentrated round of speech work landed this month.

[parakeet.cpp](https://github.com/mudler/parakeet.cpp), our ASR engine, gained:

- [NeMo-faithful segment timestamps](https://github.com/mudler/LocalAI/pull/10207)
- [a multilingual streaming Nemotron-3.5 model](https://github.com/mudler/LocalAI/pull/10199)
- [dynamic batching for concurrent transcription](https://github.com/mudler/LocalAI/pull/10112)
- [CUDA graphs](https://github.com/mudler/LocalAI/pull/10273)

Alongside it, the new [CrispASR backend](https://github.com/mudler/LocalAI/pull/10099) adds multi-architecture ASR and TTS, and [60 Piper TTS voices across 42 languages](https://github.com/mudler/LocalAI/pull/10296) land in the gallery, together with [per-request TTS instructions and parameters](https://github.com/mudler/LocalAI/pull/10172).
