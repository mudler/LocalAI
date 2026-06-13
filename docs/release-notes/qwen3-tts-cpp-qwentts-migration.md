# qwen3-tts-cpp backend now powered by qwentts.cpp

The `qwen3-tts-cpp` backend has migrated from `predict-woo/qwen3-tts.cpp` to
[`ServeurpersoCom/qwentts.cpp`](https://github.com/ServeurpersoCom/qwentts.cpp)
(MIT). New in this release:

- **Streaming TTS** (`stream: true` on `/v1/audio/speech`).
- **Named speakers** (CustomVoice): set `voice` to `serena`, `vivian`, `ryan`,
  `aiden`, `eric`, `dylan`, ...
- **Voice design**: set `instructions` to a free-text attribute string
  (e.g. "male, young adult, moderate pitch") with a VoiceDesign model.
- **Voice cloning**: set `voice` to a 24kHz reference `.wav` path; optionally
  pass `ref_text` in the request params for in-context cloning.
- **1.7B models** and **Q8_0 / Q4_K_M** quantizations.

## Breaking change

The GGUF format differs. The previous `endo5501/qwen3-tts.cpp` F16 weights are
**not compatible** with the new backend. Re-install a `qwen3-tts-cpp*` model
from the gallery (now served from `Serveurperso/Qwen3-TTS-GGUF`) to upgrade.
