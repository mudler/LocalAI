---
title: "LocalAI 4.2: who spoke when, and whose face is that"
date: 2026-05-11
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "diarization", "voice-recognition", "face-recognition", "ollama", "backends"]
summary: "A /v1/audio/diarization endpoint, voice and face recognition with liveness, a drop-in Ollama API, and eleven new backends."
extracss: ["blog.css"]
---

You record an hour of standup, run it through Whisper, and get back one long wall of text. Every word is correct. You still have no idea who said any of them, so you end up scrubbing through the audio with the transcript open in another window, guessing at voices.

4.2.0 is mostly about that class of problem. Audio and images carry more than "here are the words" or "here is a picture", and until now LocalAI had nowhere to put the rest of it.

## Who spoke when

There is a new `/v1/audio/diarization` endpoint, shaped like `/v1/audio/transcriptions` so your existing multipart code mostly carries over:

```bash
curl http://localhost:8080/v1/audio/diarization \
  -H "Content-Type: multipart/form-data" \
  -F file="@meeting.wav" \
  -F model="vibevoice-cpp-asr" \
  -F num_speakers=3
```

```json
{
  "task": "diarize",
  "duration": 12.34,
  "num_speakers": 2,
  "segments": [
    {"id": 0, "speaker": "SPEAKER_00", "label": "0", "start": 0.00, "end": 2.34},
    {"id": 1, "speaker": "SPEAKER_01", "label": "1", "start": 2.34, "end": 4.10}
  ]
}
```

Two backends serve it. [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) does pure diarization (pyannote-3.0 segmentation, a speaker-embedding extractor, then clustering) and never transcribes, so you do not pay for ASR you did not ask for. `vibevoice-cpp` emits speaker-labelled segments as a by-product of its long-form ASR pass, so with `include_text=true` you get a transcript per segment for free! `response_format` gives you `json`, `verbose_json`, or `rttm` if you want to feed the output to `dscore`.

One thing to know before you build on it: `SPEAKER_00` is local to a single request. Run the same meeting twice and the numbering can come out differently, and nothing promises that `SPEAKER_00` in Monday's recording is the same human as `SPEAKER_00` in Tuesday's. If you need identity across files, pair it with `/v1/voice/embed` and keep your own embedding store. Which brings me to..

## Voices and faces

`/v1/voice/*` is new ([#9500](https://github.com/mudler/LocalAI/pull/9500)): verify (are these two clips the same person?), identify (which of my enrolled speakers is this?), embed (give me the vector, I will do the rest myself), and analyze (age, gender, emotion).

```bash
local-ai models install speechbrain-ecapa-tdnn

curl -sX POST http://localhost:8080/v1/voice/verify \
  -H "Content-Type: application/json" \
  -d '{
    "model": "speechbrain-ecapa-tdnn",
    "audio1": "https://example.com/alice_1.wav",
    "audio2": "https://example.com/alice_2.wav"
  }'
```

```json
{"verified": true, "distance": 0.18, "threshold": 0.25}
```

The default threshold is around 0.25 for ECAPA-TDNN, and it moves per engine, so pass `threshold` explicitly if you swap the model out.

`/v1/face/*` does the same thing for faces ([#9480](https://github.com/mudler/LocalAI/pull/9480)), plus detection and demographics, and 4.2.0 adds antispoofing. Holding a printed photo or a phone screen up to the camera is the oldest attack on face auth there is, and the liveness check rejects it.

Some honest limits. Liveness is an arms race and this is not bank-grade. The demographic heads emit confident-looking numbers for age and emotion that you should read as a rough signal and not as a fact about a person. And the default `insightface` buffalo packs are released for non-commercial research use only, so if you are shipping this in a product, pick the OpenCV Zoo entry instead. That is in the docs, but people skip docs, so it is here too.

The samples never leave your machine, which is the part I actually care about. They go from your process to the backend running next to it and nowhere else. Doing biometrics against somebody else's cloud API always felt like the worst possible trade.

## Point your ollama client at LocalAI

```sh
OLLAMA_HOST=http://localhost:8080 ollama run qwen3
```

LocalAI answers the Ollama API now ([#9284](https://github.com/mudler/LocalAI/pull/9284)), so a tool that only ever learned to talk to Ollama keeps working with no code change on your side. `/api/chat`, `/api/generate`, `/api/embed`, `/api/tags`, `/api/show`, `/api/ps` and `/api/version` all land on the engine you were already running, and your existing `/v1/*` clients are untouched.

There is no `/api/pull` in there. Models come from the LocalAI gallery or from a URL you hand it, so `ollama run` against something you have not installed yet will not go and fetch it for you.

## Video, and an interface repaint

`stable-diffusion.ggml` generates video now ([#9420](https://github.com/mudler/LocalAI/pull/9420))! There are gallery entries for Wan 2.1 FLF2V 14B 720P and Wan i2v 720p, including first-last-frame interpolation.

The React interface got a long cycle of work. The chat is redesigned, the palette moved to Nord, and there is i18n across English, Italiano, Español, Deutsch and 简体中文. You can brand your instance too - name, tagline, logo, favicon - and the login page, sidebar, footer and browser tab all pick it up. Handy if you run LocalAI for a team and would rather it did not look like somebody's side project.

The model config editor is interactive now, with autocomplete over known fields and live validation, and it renames the file on save so you stop accumulating three copies of the same config.

## Eleven new backends

sglang, ik-llama.cpp, TurboQuant, sam.cpp, Kokoros, qwen3tts.cpp, tinygrad-multimodal (experimental, do not build anything load-bearing on it yet), vibevoice.cpp, LocalVQE, insightface, and voice-rec.

vLLM reached feature parity with llama.cpp in this cycle. The full `AsyncEngineArgs` surface is exposed as a generic YAML map, and tensor-parallel distributed workers let a single model span nodes. There are CUDA 13 builds for vLLM, vLLM-omni and sglang, plus L4T arm64 for Jetson-class boards.

## The unglamorous half

Most of the 279 pull requests here are not features. A sample of what actually went in:

- llama.cpp renamed its `common` target to `llama-common`, which broke the TurboQuant build until the detection was fixed.
- ik-llama.cpp needed a patch to `clip.cpp` for the new `ggml_quantize_chunk` signature, plus adapting to the `common_grammar` struct in `sampling.h`.
- `mlx-vlm` is pinned to v0.4.4 to unblock CUDA builds.
- vLLM dropped the flash-attn wheel to avoid a torch 2.10 ABI mismatch.
- Whisper transcriptions can be cancelled by the client, through the ggml `abort_callback`, so aborting a request frees the GPU instead of letting it run to completion in the background.
- faster-whisper emits word-level timestamps.
- gfx1151 (Strix Halo / Ryzen AI MAX) works, with `AMDGPU_TARGETS` exposed as a build-arg.

On the security side: an unsafe `sprintf()` came out of the C++ grpc-server, env-supplied API keys are stripped from Settings API requests before they get persisted so they cannot leak back out through the config, and deleting a user on PostgreSQL cascades across everything they owned instead of leaving orphaned rows behind.

Distributed mode got a hardening pass. Round-robin across replicas of the same model, "Upgrade All" scoped to the nodes that actually have the backend installed, NATS `backend.upgrade` split off from install, and correct VRAM/RAM reporting on NVIDIA unified-memory hosts.

## Thanks

This one had a lot of hands on it. Thanks to @richiejp for the model config editor, Kokoros and a pile of build fixes, @Anai-Guo, @russell, @leinasi2014, @keithmattix for gfx1151, @orbisai0security and @SAY-5 for the security work, @walcz-de, @thelittlefireman, @sec171, @pjbrzozowski, @mvanhorn, @arteven, @Dennisadira, @eglia, @arbrick, @neurocis and @ER-EPR.

If you are wiring up diarization or the voice endpoints and get stuck, open an issue or reach out, I am genuinely happy to help you get it working.

[Full release notes](https://github.com/mudler/LocalAI/releases/tag/v4.2.0).
