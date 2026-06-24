---
title: "Whisper-Medusa"
description: "Run Whisper-Medusa speech-to-text checkpoints with LocalAI"
---

The `whisper-medusa` backend serves aiola's Whisper-Medusa checkpoints through
LocalAI's OpenAI-compatible audio transcription endpoint. Whisper-Medusa uses
multiple decoding heads to predict several tokens per decoding step.

## Model configuration

Install the `whisper-medusa` backend from the backend gallery, then create a
model configuration such as:

```yaml
name: whisper-medusa
backend: whisper-medusa
parameters:
  model: aiola/whisper-medusa-linear-libri
options:
  - language:en
  - regulation_start:140
  - regulation_factor:1.01
```

Transcribe a clip with the standard endpoint:

```bash
curl http://localhost:8080/v1/audio/transcriptions \
  -F file=@audio.wav \
  -F model=whisper-medusa \
  -F language=en
```

`language` in the request overrides the configured default. The regulation
options control the exponential length penalty passed to upstream generation.

{{% notice warning %}}
The upstream Whisper-Medusa repository is archived. Its implementation accepts
clips of at most 30 seconds and expects 16 kHz audio; LocalAI resamples input to
16 kHz and rejects longer clips. The LibriSpeech checkpoints are optimized for
English. Use `aiola/whisper-medusa-multilingual` for supported multilingual
audio.
{{% /notice %}}

The backend currently ships Linux CPU and NVIDIA CUDA 12 images. It is not
advertised for macOS, ROCm, Intel GPU, or Jetson because upstream pins PyTorch
2.2.2 and has not published compatibility for those platforms.
