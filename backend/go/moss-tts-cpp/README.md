# MOSS-TTS C++ backend

This backend runs the OpenMOSS **MOSS-TTS-Local (v1.5)** GGUF model through
[moss-tts.cpp](https://github.com/mudler/moss-tts.cpp), a from-scratch C++/ggml
port with no Python at inference time. It generates **48 kHz stereo** speech and
supports reference-audio voice cloning.

The engine loads three GGUFs: the local transformer (the model), the
MOSS-Audio-Tokenizer neural codec, and the text tokenizer. It is loaded via
purego (cgo-less `dlopen`) exactly like `qwen3-tts-cpp`.

## Model configuration

The model path points at the local transformer GGUF. The codec and text
tokenizer are auto-discovered as siblings of the model:

- codec: a `*.gguf` whose name contains `audio` + `tokenizer` (or `codec`),
  e.g. `moss-audio-tokenizer-v2-f32.gguf`
- tokenizer: the other `*.gguf` whose name contains `tokenizer`,
  e.g. `moss-tokenizer-v1_5.gguf`

```yaml
name: moss-tts-cpp
backend: moss-tts-cpp
parameters:
  model: moss-tts-local-v1_5-q8_0.gguf
known_usecases:
  - tts
tts:
  audio_path: voices/default-reference.wav  # optional model-wide clone reference
```

Override discovery when the filenames are non-standard:

```yaml
options:
  - "codec:moss-audio-tokenizer-v2-f32.gguf"
  - "tokenizer:moss-tokenizer-v1_5.gguf"
  - "seed:42"
```

GGUFs for the Local v1.5 model (plus its codec and tokenizer) live at
[mudler/MOSS-TTS-Local-Transformer-v1.5-GGUF](https://huggingface.co/mudler/MOSS-TTS-Local-Transformer-v1.5-GGUF).

## Voice cloning

MOSS-TTS-Local has no named speakers; cloning is driven purely by a
reference-audio **path**. Request precedence is: a request `voice` that ends in
a known audio extension (`.wav`, `.flac`, `.mp3`, `.ogg`, `.m4a`), then
`tts.audio_path`. The engine decodes the reference itself, so no client-side
resampling is required.

## API example

```bash
curl http://localhost:8080/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "moss-tts-cpp",
    "input": "This request uses a saved reference voice.",
    "voice": "/path/to/reference.wav"
  }' \
  --output speech.wav
```

## Native end-to-end test

The labeled test loads real GGUFs, synthesizes a 48 kHz stereo WAV, and streams
audio:

```bash
make -C backend/go/moss-tts-cpp moss-tts-cpp

MOSSTTS_MODEL=/path/to/moss-tts-local-v1_5-q8_0.gguf \
MOSSTTS_CODEC=/path/to/moss-audio-tokenizer-v2-f32.gguf \
MOSSTTS_TOKENIZER=/path/to/moss-tokenizer-v1_5.gguf \
MOSSTTS_LIBRARY=backend/go/moss-tts-cpp/libgomosstts-cpp-fallback.so \
  go test ./backend/go/moss-tts-cpp -ginkgo.label-filter=e2e
```
