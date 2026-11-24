# Magpie TTS C++ backend

This backend runs NVIDIA's **Magpie TTS Multilingual 357M** GGUF through
[magpie-tts.cpp](https://github.com/mudler/magpie-tts.cpp), a from-scratch
C++/ggml port (model + NanoCodec + tokenizer + G2P dictionaries in one
self-contained GGUF, no Python at inference time). It generates **22.05 kHz
mono** speech in 5 baked voices across 9+ languages.

The library is loaded via purego (cgo-less `dlopen`) exactly like
`qwen3-tts-cpp` / `moss-tts-cpp`; the flat C-API (`magpie_tts_capi_*`) is
exported directly by the upstream shared library, so there is no local C shim.

## Model configuration

The model path points at the single GGUF:

```yaml
name: magpie-tts-cpp
backend: magpie-tts-cpp
parameters:
  model: magpie-tts-multilingual-357m-q8_0.gguf
known_usecases:
  - tts
options:
  - "speaker:Aria"   # optional default voice (Aria, Jason, John, Leo, Sofia, or 0-4)
  - "language:en"    # optional default language
```

GGUFs live at
[mudler/magpie-tts.cpp-gguf](https://huggingface.co/mudler/magpie-tts.cpp-gguf)
(q8_0 recommended: near-lossless, ~624 MB, fastest decode).

## Voices and languages

Magpie has 5 baked speakers - `Aria`, `Jason`, `John`, `Leo`, `Sofia` - and no
voice cloning. The request `voice` accepts the names case-insensitively or the
indices `0`-`4`; empty selects Aria. Languages: `en`, `es`, `de`, `fr`, `it`,
`pt-BR`, `hi`, `vi`, `ko`, `ar-AE`, `ar-SA`, `ar-MSA` (case-insensitive;
default `en`).

## API example

```bash
curl http://localhost:8080/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "magpie-tts-cpp",
    "input": "Hello world, this is a test of the text to speech system.",
    "voice": "sofia",
    "language": "en"
  }' \
  --output speech.wav
```

## Native end-to-end test

The labeled test loads a real GGUF, synthesizes WAVs (verifying rate, layout
and non-silence), and exercises the streaming path:

```bash
make -C backend/go/magpie-tts-cpp magpie-tts-cpp

MAGPIETTS_MODEL=/path/to/magpie-tts-multilingual-357m-q8_0.gguf \
MAGPIETTS_LIBRARY=backend/go/magpie-tts-cpp/libgomagpiettscpp-fallback.so \
  go test ./backend/go/magpie-tts-cpp -ginkgo.label-filter=e2e
```

`bash test.sh` does the same and auto-downloads the q8_0 GGUF when
`MAGPIETTS_MODEL` is unset.
