+++
title = "Speaker diarization"
date = 2026-05-05
description = "A /v1/audio/diarization endpoint returning who spoke when, backed by sherpa-onnx and vibevoice-cpp."
url = "/blog/speaker-diarization/"
+++

`POST /v1/audio/diarization` is a new endpoint that returns "who spoke when" as a list of segments.

Two backends serve it:

- `sherpa-onnx` for pure diarization, combining pyannote-3.0, speaker embeddings and clustering.
- `vibevoice-cpp` for diarization bundled with long-form ASR.

Responses are available as `json`, `verbose_json` or `rttm`.

See [Audio diarization]({{% relref "features/audio-diarization" %}}). Shipped in [PR #9654](https://github.com/mudler/LocalAI/pull/9654).
