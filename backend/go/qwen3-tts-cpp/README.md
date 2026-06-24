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
