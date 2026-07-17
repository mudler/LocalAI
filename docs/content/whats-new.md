+++
disableToc = false
title = "News"
weight = 7
url = '/basics/news/'
icon = "newspaper"
+++

Release notes have been now moved completely over Github releases.

You can see the release notes [here](https://github.com/mudler/LocalAI/releases).

## 2026 Highlights

- **July 2026**: [LongCat video and avatar generation](/features/longcat-video/) — dedicated CUDA backend for `LongCat-Video` text/image-to-video and `LongCat-Video-Avatar-1.5` speech-driven avatars. Includes multi-segment continuation, portrait and recorded-audio inputs in Studio, and an SDPA CUDA 13 ARM64 build for DGX Spark.
- **April 2026**: [Audio Transform](/features/audio-transform/) — generic audio-in / audio-out endpoint with optional reference signal. First implementation: [LocalVQE](https://github.com/localai-org/LocalVQE) C++ backend (joint AEC + noise suppression + dereverberation, DeepVQE-style). Both batch (`POST /audio/transformations`) and bidirectional WebSocket streaming (`/audio/transformations/stream`). Studio "Transform" tab with synchronized waveform players for input / reference / output.
- **April 2026**: [Face recognition backend](/features/face-recognition/) — `insightface`-powered 1:1 verification, 1:N identification, face embedding, face detection, and demographic analysis. Ships both a non-commercial `buffalo_l` model and an Apache 2.0 OpenCV Zoo alternative.
- **May 2026**: [Speaker diarization](/features/audio-diarization/) — new `/v1/audio/diarization` endpoint returning "who spoke when" segments. Backed by `sherpa-onnx` (pyannote-3.0 + speaker embeddings + clustering) for pure diarization, and `vibevoice-cpp` for diarization bundled with long-form ASR. Supports `json` / `verbose_json` / `rttm` response formats.
- **June 2026**: [Sound classification](/features/audio-classification/) — new `/v1/audio/classification` endpoint for audio tagging / sound-event classification, returning scored [AudioSet](https://research.google.com/audioset/) labels (baby cry, glass breaking, alarms, ...). Backed by [ced.cpp](https://github.com/mudler/ced.cpp), a 527-class AudioSet tagger ported to ggml.
- **June 2026**: [PII analyze / redact API](/features/middleware/#analyze--redact-api) — the PII detection pipeline (NER + restricted-regex pattern tiers) is now a standalone service: `POST /api/pii/analyze` returns detected entity spans and `POST /api/pii/redact` returns the sanitised text (or `400 pii_blocked`), without routing a chat request through the middleware. Events gain an `origin` (`middleware` / `proxy` / `pii_analyze` / `pii_redact`) so `/api/pii/events` can be filtered by source.
- **July 2026**: [Model capabilities endpoint](/features/api-discovery/#model-capabilities) — `GET /v1/models/capabilities`, an additive superset of `/v1/models` that reports each model's `capabilities` plus its `input_modalities` / `output_modalities` (`text` / `image` / `audio` / `video`). Lets clients route attachments using inferred or explicitly declared model modalities instead of backend-name checks.
- **June 2026**: Concurrent scoring and PII NER on llama.cpp — the `Score` (router classifier) and `TokenClassify` (PII NER) primitives now ride llama.cpp's server task queue instead of locking the context, so they run concurrently with chat/completion/embedding traffic and with each other. The `known_usecases` restriction that forced dedicated scorer/NER model configs on llama-cpp is lifted, repeated scoring calls reuse the prompt KV cache across candidates, and scoring inputs are no longer capped by the physical batch size.

## 2024 Highlights

- **April 2024**: [Reranker API](https://github.com/mudler/LocalAI/pull/2121)
- **May 2024**: [Distributed inferencing](https://github.com/mudler/LocalAI/pull/2324), [Decentralized P2P llama.cpp](https://github.com/mudler/LocalAI/pull/2343) — [Docs](https://localai.io/features/distribute/)
- **July/August 2024**: [P2P Dashboard, Federated mode and AI Swarms](https://github.com/mudler/LocalAI/pull/2723), [P2P Global community pools](https://github.com/mudler/LocalAI/issues/3113), FLUX-1 support, [P2P Explorer](https://explorer.localai.io)
- **October 2024**: Examples moved to [LocalAI-examples](https://github.com/mudler/LocalAI-examples)
- **November 2024**: [Voice Activity Detection (VAD)](https://github.com/mudler/LocalAI/pull/4204), [Bark.cpp backend](https://github.com/mudler/LocalAI/pull/4287)
- **December 2024**: [stablediffusion.cpp backend (ggml)](https://github.com/mudler/LocalAI/pull/4289)
