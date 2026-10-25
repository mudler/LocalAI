# Qwen3-TTS C++ backend

This backend runs Qwen3-TTS GGUF models through
[qwentts.cpp](https://github.com/ServeurpersoCom/qwentts.cpp). It supports
24 kHz speech generation, streaming, named speakers, voice design, and
reference-audio cloning depending on the model variant.

## Gallery models

The following Base models accept LocalAI Voice Library profiles:

- `qwen3-tts-cpp`
- `qwen3-tts-cpp-0.6b-base-q4`
- `qwen3-tts-cpp-1.7b-base`
- `qwen3-tts-cpp-1.7b-base-q4`

Gallery models containing `customvoice` or `voicedesign` implement those Qwen
modes instead and are not advertised as raw reference-audio models.

Install a Base model with:

```bash
local-ai models install qwen3-tts-cpp
```

## Model configuration

Base filenames are detected automatically. Set `tts.voice_cloning` only when a
verified private conversion has a name that does not identify it as a Base or
VoiceClone model:

```yaml
name: private-qwen-voice
backend: qwen3-tts-cpp
parameters:
  model: qwen-private/talker.gguf
known_usecases:
  - tts
tts:
  voice_cloning: true
  audio_path: voices/default-reference.wav  # optional model-wide fallback
```

The tokenizer GGUF is auto-discovered when its filename contains `tokenizer`
and it is stored beside the talker. Otherwise set
`options: ["tokenizer:qwen-private/tokenizer.gguf"]`.

`tts.voice_cloning: false` removes a model from Voice Library compatibility
results and rejects saved `localai://voice-profiles/...` references. It does
not disable Qwen's named-speaker or VoiceDesign modes. Setting it to `true`
cannot add cloning to a backend that lacks LocalAI's reference-audio contract.

### Concurrent requests (`max_batch`)

By default one request is synthesized at a time. `max_batch:N` (alias
`parallel:N`) initializes qwentts with `max_batch = N`: its compute worker
coalesces up to N concurrent `/v1/audio/speech` requests (streaming or not)
into batched decode steps, and LocalAI stops serializing requests to the
backend. Further requests queue FIFO inside qwentts.

```yaml
options:
  - max_batch:4
```

Measured with the 1.7B Base Q4_K_M talker and four concurrent streaming
requests (time to the first PCM chunk): RTX 5060 Ti 0.02/0.36/0.70/1.04 s with
`max_batch:1`, 0.06 s for all four with `max_batch:4`. VRAM grows with N
(about 2.6 GB at 1, 6 GB at 4, 10.5 GB at 8 for that talker).

- Incremental synthesis (`TTSLive`, the realtime pipeline with
  `streaming.tts: true`) requires `max_batch:1` and is rejected otherwise
  (qwentts `docs/INCREMENTAL.md`).
- ROCm on gfx1201 (RDNA4) hangs the GPU in hipBLASLt for batched matmuls;
  `env: {ROCBLAS_USE_HIPBLASLT: "0"}` in the model config routes them to
  rocBLAS. CUDA needs no workaround.

Request precedence is: a request `voice`, then `tts.voice`, then
`tts.audio_path`. A saved profile supplies its private WAV and exact transcript
for that request without changing the model YAML.

## API example

Create or select a profile in **Operate → Voice Library**, then pass its stable
URI to either speech endpoint:

```bash
curl http://localhost:8080/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "qwen3-tts-cpp",
    "input": "This request uses a saved reference voice.",
    "voice": "localai://voice-profiles/PROFILE_ID"
  }' \
  --output speech.wav
```

## Native end-to-end test

The labeled test loads real GGUFs, synthesizes speech, streams audio, and
exercises cloning with a generated 24 kHz reference WAV:

```bash
make -C backend/go/qwen3-tts-cpp qwen3-tts-cpp

QWEN3TTS_MODEL=/path/to/qwen-talker-0.6b-base-Q8_0.gguf \
QWEN3TTS_CODEC=/path/to/qwen-tokenizer-12hz-Q8_0.gguf \
QWEN3TTS_LIBRARY=backend/go/qwen3-tts-cpp/libgoqwen3ttscpp-fallback.so \
  go test ./backend/go/qwen3-tts-cpp -ginkgo.label-filter=e2e
```

### Incremental text and audio

The `TTSLive` backend RPC accepts text fragments while emitting audio from one
persistent utterance. The first `TTSRequest` carries voice, language and sampling
options; subsequent requests carry only `text`. Each fragment must contain valid UTF-8;
the protobuf string boundary does not accept incomplete Unicode characters. Closing the request stream commits
end of input. Continue reading responses until EOF to receive the codec tail.
Responses contain one streaming WAV header followed by mono PCM16 chunks at 24 kHz.

Realtime audio pipelines select this path when their TTS model uses
`qwen3-tts-cpp` and `pipeline.streaming.llm` and `pipeline.streaming.tts` are
`true`. Cleaned LLM content feeds the same synthesis session; clause splitting is
bypassed. WebSocket and WebRTC transports receive audio as it becomes available.
The text queue is bounded, so slow audio consumers apply backpressure to text
generation. Audio emits before the full response is available; WebSocket
conversation history still retains the final audio, as in the existing path. Cancellation stops input and audio. A failed live session is not
retried because consumed text and emitted speech cannot be replayed safely.

This requires the incremental-input native fork. Its initial implementation uses
one native batch slot and commits whitespace-delimited words or individual CJK, Kana and
Hangul characters. Incomplete words remain buffered until a stable boundary. Reference audio without `ref_text` is supported; transcript-based ICL
cloning is rejected for incremental sessions. Other TTS backends retain their
existing whole-text or clause synthesis paths. The standard speech HTTP request
continues to accept a complete JSON input.

To run the real native streaming check, provide `QWEN3TTS_LIBRARY`,
`QWEN3TTS_MODEL`, `QWEN3TTS_CODEC`, and `QWEN3TTS_LIVE_TEST=1`, then run
`go test ./backend/go/qwen3-tts-cpp -ginkgo.focus TTSLiveNative -v` from the
repository root. It withholds late text and end of input until real PCM arrives.
