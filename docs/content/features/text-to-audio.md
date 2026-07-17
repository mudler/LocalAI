
+++
disableToc = false
title = "Text to Audio (TTS)"
weight = 11
url = "/features/text-to-audio/"
+++

## API Compatibility

The LocalAI TTS API is compatible with the [OpenAI TTS API](https://platform.openai.com/docs/guides/text-to-speech) and the [Elevenlabs](https://api.elevenlabs.io/docs) API.

## LocalAI API

The `/tts` endpoint can also be used to generate speech from text.

## Usage

Input: `input`, `model`

For example, to generate an audio file, you can send a POST request to the `/tts` endpoint with the instruction as the request body:

```bash
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{
  "input": "Hello world",
  "model": "tts"
}'
```

Returns an `audio/wav` file.

## Voice Library

Administrators can manage reusable voice-cloning references from **Operate → Voice Library** in the LocalAI WebUI. The library replaces per-model filesystem and YAML setup for supported cloning backends:

1. Select **Create voice** and upload or record a clear reference clip.
2. Enter the exact words spoken in the clip. The transcript is sent to backends that require reference text.
3. Confirm that you have permission to clone the voice, then save the profile.
4. Open **Text to Speech**, choose a model marked **Cloning ready**, and select the saved voice.

The browser converts uploads and recordings to mono, 24 kHz, 16-bit PCM WAV so the same profile works across compatible backends. Clips must be between 1 and 120 seconds and no larger than 50 MiB; 6–30 seconds of clean, single-speaker audio is recommended. Profile audio is private biometric source material: LocalAI stores it below its configured data path, serves previews only to authenticated TTS users, and never returns its filesystem path.

### Voice profile API

The WebUI uses the following endpoints. Creating and deleting profiles requires administrator access; listing profiles and playing previews requires access to the TTS feature.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| `GET` | `/api/voice-profiles` | List saved profiles and their public metadata. |
| `POST` | `/api/voice-profiles` | Create a profile from multipart form data or JSON with base64 audio. |
| `GET` | `/api/voice-profiles/{id}/audio` | Stream the authenticated WAV preview, including range requests. |
| `DELETE` | `/api/voice-profiles/{id}` | Permanently delete a profile. |

For example, an administrator can create a profile without the WebUI:

```bash
curl http://localhost:8080/api/voice-profiles \
  -F 'name=Documentary narrator' \
  -F 'language=en-US' \
  -F 'transcript=The exact words spoken in this reference.' \
  -F 'consent_confirmed=true' \
  -F 'audio=@reference.wav;type=audio/wav'
```

The response includes an opaque voice reference such as `localai://voice-profiles/550e8400-e29b-41d4-a716-446655440000`. Pass that value as `voice` to either TTS-compatible endpoint:

```bash
curl http://localhost:8080/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "qwen3-tts-base",
    "input": "This sentence will use the saved voice.",
    "voice": "localai://voice-profiles/550e8400-e29b-41d4-a716-446655440000"
  }' --output speech.wav
```

LocalAI resolves the opaque reference only for models that advertise voice-cloning support. Existing named speakers, backend-specific voice IDs, and explicit model YAML voice configuration remain available for models and advanced workflows that do not use the library.

The Voice Library uses the same server-side capability resolver for installed models and gallery recommendations. Administrators can inspect the currently configured galleries without maintaining a separate backend list:

```bash
curl 'http://localhost:8080/api/models?capability=voice_cloning&items=20'
```

Each returned model includes a non-null `voice_cloning` contract. Variant checks are applied before the model is returned, so TTS-only CustomVoice, VoiceDesign, or preset-prompt variants are not offered as reference-audio models. When the WebUI detects that no compatible model is installed, it uses this response to offer direct installation.

### Model configuration

Voice Library support is automatic for known backends and model variants. A custom model can override that detection with `tts.voice_cloning`:

```yaml
name: private-qwen-base
backend: qwen3-tts-cpp
parameters:
  model: private/qwen-talker-checkpoint.gguf
known_usecases:
  - tts
tts:
  # Optional: omit this for automatic backend and variant detection.
  voice_cloning: true
  # Optional model-wide fallback when a request does not select a saved profile.
  audio_path: voices/default-reference.wav
options:
  - tokenizer:private/qwen-tokenizer.gguf
```

`tts.voice_cloning` has three states:

| Value | Behavior |
| --- | --- |
| omitted | Detect support from the backend plus `name`, `parameters.model`, and compatibility options. This is recommended for gallery models. |
| `true` | Advertise Voice Library support for a custom-named variant of a backend that LocalAI already knows can clone voices. This cannot add cloning to an unsupported backend. |
| `false` | Hide the model from Voice Library compatibility results and reject `localai://voice-profiles/...` references for it. Backend-specific named voices and manual reference paths remain available. |

The older `options: ["voice_cloning:true"]` and `options: ["voice_cloning:false"]` spellings remain accepted for compatibility. Prefer `tts.voice_cloning`; generic options may be forwarded to a backend, whereas the typed field is consumed only by LocalAI.

Reference selection follows this order:

1. A request `voice`, including a saved `localai://voice-profiles/...` URI.
2. The model's `tts.voice` default.
3. The model's `tts.audio_path` reference-audio fallback.
4. A backend-specific default voice or option.

When a saved profile is selected, LocalAI supplies both its private WAV and exact transcript for that request. It does not rewrite the model YAML or copy the recording into the model directory.

#### Supported backend and model variants

| Backend | Automatically compatible variants |
| --- | --- |
| `chatterbox`, `faster-qwen3-tts`, `fish-speech`, `moss-tts-cpp`, `neutts`, `omnivoice-cpp`, `pocket-tts`, `voxcpm` | Reference-audio cloning models served by these dedicated backends. |
| `qwen-tts`, `qwen3-tts-cpp`, `vllm-omni` | Base or VoiceClone variants. CustomVoice and VoiceDesign variants are not raw reference-audio models. |
| `vibevoice-cpp` | 1.5B reference-WAV variants. The realtime 0.5B preset-prompt model is excluded. |
| `coqui` | XTTS and YourTTS variants. |
| `crispasr` | F5-TTS variants. ASR, Piper, Orpheus, and other CrispASR model families are excluded. |

This table describes the built-in resolver, not a frontend allowlist. Gallery entries and installed configs are evaluated by the server, and `tts.voice_cloning` can make a verified custom filename explicit.

## Streaming TTS

LocalAI supports streaming TTS generation, allowing audio to be played as it's generated. This is useful for real-time applications and reduces latency.

To enable streaming, add `"stream": true` to your request:

```bash
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{
  "input": "Hello world, this is a streaming test",
  "model": "voxcpm",
  "stream": true
}' | aplay
```

The audio will be streamed chunk-by-chunk as it's generated, allowing playback to start before generation completes. This is particularly useful for long texts or when you want to minimize perceived latency.

You can also pipe the streamed audio directly to audio players like `aplay` (Linux) or save it to a file:

```bash
# Stream to aplay (Linux)
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{
  "input": "This is a longer text that will be streamed as it is generated",
  "model": "voxcpm",
  "stream": true
}' | aplay

# Stream to a file
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{
  "input": "Streaming audio to file",
  "model": "voxcpm",
  "stream": true
}' > output.wav
```

Note: Streaming TTS is currently supported by the `voxcpm` backend. Other backends will fall back to non-streaming mode if streaming is not supported.

## Backends

### 🐸 Coqui

Required: Don't use `LocalAI` images ending with the `-core` tag,. Python dependencies are required in order to use this backend.

Coqui works without any configuration, to test it, you can run the following curl command:

```
    curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
        "backend": "coqui",
        "model": "tts_models/en/ljspeech/glow-tts",
        "input":"Hello, this is a test!"
        }'
```

You can use the env variable COQUI_LANGUAGE to set the language used by the coqui backend.

You can also use config files to configure tts models (see section below on how to use config files).

### Piper

To install the `piper` audio models manually:

- Download Voices from https://github.com/rhasspy/piper/releases/tag/v0.0.2
- Extract the `.tar.tgz` files (.onnx,.json) inside `models`
- Run the following command to test the model is working

To use the tts endpoint, run the following command. You can specify a backend with the `backend` parameter. For example, to use the `piper` backend:
```bash
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{
  "model":"it-riccardo_fasol-x-low.onnx",
  "backend": "piper",
  "input": "Ciao, sono Ettore"
}' | aplay
```

Note:

- `aplay` is a Linux command. You can use other tools to play the audio file.
- The model name is the filename with the extension.
- The model name is case sensitive.
- LocalAI must be compiled with the `GO_TAGS=tts` flag.

### Transformers-musicgen

LocalAI also has experimental support for `transformers-musicgen` for the generation of short musical compositions. Currently, this is implemented via the same requests used for text to speech:

```
curl --request POST \
  --url http://localhost:8080/tts \
  --header 'Content-Type: application/json' \
  --data '{
    "backend": "transformers-musicgen",
    "model": "facebook/musicgen-medium",
    "input": "Cello Rave"
}' | aplay
```

Future versions of LocalAI will expose additional control over audio generation beyond the text prompt.

### ACE-Step

[ACE-Step 1.5](https://github.com/ACE-Step/ACE-Step-1.5) is a music generation model that can create music from text descriptions, lyrics, or audio samples. It supports both simple text-to-music and advanced music generation with metadata like BPM, key scale, and time signature.

#### Setup

Install the `ace-step-turbo` model from the Model gallery or run `local-ai models install ace-step-turbo`.

#### Usage

ACE-Step supports two modes: **Simple mode** (text description + vocal language) and **Advanced mode** (caption, lyrics, BPM, key, and more).

**Simple mode:**
```bash
curl http://localhost:8080/v1/audio/speech -H "Content-Type: application/json" -d '{
  "model": "ace-step-turbo",
  "input": "A soft Bengali love song for a quiet evening",
  "vocal_language": "bn"
}' --output music.flac
```

**Advanced mode** (using the `/v1/sound-generation` endpoint):
```bash
curl http://localhost:8080/v1/sound-generation -H "Content-Type: application/json" -d '{
  "model": "ace-step-turbo",
  "caption": "A funky Japanese disco track",
  "lyrics": "[Verse 1]\n...",
  "bpm": 120,
  "keyscale": "Ab major",
  "language": "ja",
  "duration_seconds": 225
}' --output music.flac
```

#### Configuration

You can configure ACE-Step models with various options:

```yaml
name: ace-step-turbo
backend: ace-step
parameters:
  model: acestep-v15-turbo
known_usecases:
  - sound_generation
  - tts
options:
  - "device:auto"
  - "use_flash_attention:true"
  - "init_lm:true"  # Enable LLM for enhanced generation
  - "lm_model_path:acestep-5Hz-lm-0.6B"  # or acestep-5Hz-lm-4B
  - "lm_backend:pt"  # or vllm
  - "temperature:0.85"
  - "top_p:0.9"
  - "inference_steps:8"
  - "guidance_scale:7.0"
```

### VibeVoice

[VibeVoice-Realtime](https://github.com/microsoft/VibeVoice) is a real-time text-to-speech model that generates natural-sounding speech from precomputed voice presets.

#### Setup

Install the `vibevoice` model in the Model gallery or run `local-ai models install vibevoice`.

#### Usage

Use the tts endpoint by specifying the vibevoice backend:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "vibevoice",
     "input":"Hello!"
   }' | aplay
```

#### Voice presets

The Python `vibevoice` realtime 0.5B model uses `.pt` voice preset files. You can configure a model with a specific preset:

```yaml
name: vibevoice
backend: vibevoice
parameters:
  model: microsoft/VibeVoice-Realtime-0.5B
tts:
  voice: "Frank"  # or use audio_path to specify a .pt file path
  # Available English voices: Carter, Davis, Emma, Frank, Grace, Mike
```

{{% notice note %}}
The realtime 0.5B preset model is not advertised to the Voice Library because it does not accept a raw reference WAV per request. For Voice Library profiles, use a `vibevoice-cpp` 1.5B reference-WAV model; LocalAI detects the 1.5B variant automatically, or a custom name can set `tts.voice_cloning: true`.
{{% /notice %}}

Then you can use the model:

```bash
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{
     "model": "vibevoice",
     "input":"Hello!"
   }' | aplay
```

### OmniVoice

[OmniVoice](https://github.com/ServeurpersoCom/omnivoice.cpp) (`omnivoice-cpp` backend) is a native C++ / GGML text-to-speech engine. It supports voice cloning (from reference audio plus its transcript), voice design (steering the voice with attribute keywords such as gender, age, pitch, style, volume, and emotion), and streaming synthesis. Output is 24kHz mono audio and it covers 646 languages.

#### Setup

Install the `omnivoice-cpp` model in the Model gallery or run `local-ai models install omnivoice-cpp`. A higher-quality BF16 variant is available as `omnivoice-cpp-hq` (the default `omnivoice-cpp` ships Q8_0 GGUFs).

#### Usage

Use the speech endpoint by specifying the omnivoice-cpp backend:

```bash
curl http://localhost:8080/v1/audio/speech -H "Content-Type: application/json" -d '{
     "model": "omnivoice-cpp",
     "input": "Hello world, this is a test."
   }' | aplay
```

#### Voice cloning

Pass a reference audio file via the `voice` parameter and its transcript via the `ref_text` generation parameter:

```bash
curl http://localhost:8080/v1/audio/speech -H "Content-Type: application/json" -d '{
     "model": "omnivoice-cpp",
     "input": "Hello world, this is a test.",
     "voice": "path/to/reference_audio.wav",
     "params": { "ref_text": "This is the transcript of the reference audio." }
   }' | aplay
```

You can also pin a default cloned voice in the model config so callers do not have to pass it on every request. Both `tts.voice` and `tts.audio_path` are honored as the reference audio (a per-request `voice` overrides them); paths are resolved relative to the model directory:

```yaml
name: omnivoice-cpp
backend: omnivoice-cpp
parameters:
  model: omnivoice-cpp/omnivoice-base-Q8_0.gguf
tts:
  voice_cloning: true                    # optional explicit declaration; gallery models are auto-detected
  audio_path: "voices/my_reference.wav"   # default cloning reference (or use tts.voice)
options:
  - "tokenizer:omnivoice-cpp/omnivoice-tokenizer-Q8_0.gguf"
```

#### Voice design

Steer the synthesized voice with attribute keywords (gender, age, pitch, style, volume, emotion) by passing an `instructions` string per request:

```bash
curl http://localhost:8080/v1/audio/speech -H "Content-Type: application/json" -d '{
     "model": "omnivoice-cpp",
     "input": "Hello world, this is a test.",
     "instructions": "female young high soft emotion:happy"
   }' | aplay
```

#### Configuration

The backend loads the base GGUF from `parameters.model` and its tokenizer from the `tokenizer:` option. A few optional generation knobs are available as `options`:

```yaml
name: omnivoice-cpp
backend: omnivoice-cpp
parameters:
  model: omnivoice-cpp/omnivoice-base-Q8_0.gguf
options:
  - "tokenizer:omnivoice-cpp/omnivoice-tokenizer-Q8_0.gguf"
  - "use_fa:true"      # enable flash attention
  - "clamp_fp16:true"  # clamp activations for fp16 stability
  - "seed:42"          # deterministic generation
  - "denoise:true"     # denoise the generated audio
```

A per-request `seed` can also be supplied through the `params` map alongside `ref_text`.

### Pocket TTS

[Pocket TTS](https://github.com/kyutai-labs/pocket-tts) is a lightweight text-to-speech model designed to run efficiently on CPUs. It supports voice cloning through HuggingFace voice URLs or local audio files.

#### Setup

Install the `pocket-tts` model in the Model gallery or run `local-ai models install pocket-tts`.

#### Usage

Use the tts endpoint by specifying the pocket-tts backend:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "pocket-tts",
     "input":"Hello world, this is a test."
   }' | aplay
```

#### Voice cloning

Pocket TTS supports voice cloning through built-in voice names, HuggingFace URLs, or local audio files. You can configure a model with a specific voice:

```yaml
name: pocket-tts
backend: pocket-tts
tts:
  voice: "azelma"  # Built-in voice name
  # Or use HuggingFace URL: "hf://kyutai/tts-voices/alba-mackenna/casual.wav"
  # Or use local file path: "path/to/voice.wav"
  # Available built-in voices: alba, marius, javert, jean, fantine, cosette, eponine, azelma
```

To make a reference recording the model-wide fallback, use `tts.audio_path`. The gallery model is detected automatically; `tts.voice_cloning` is only needed when you want an explicit declaration:

```yaml
name: pocket-tts-clone
backend: pocket-tts
tts:
  voice_cloning: true
  audio_path: "voices/reference.wav"
```

You can also pre-load a default voice for faster first generation:

```yaml
name: pocket-tts
backend: pocket-tts
options:
  - "default_voice:azelma"  # Pre-load this voice when model loads
```

Then you can use the model:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "pocket-tts",
     "input":"Hello world, this is a test."
   }' | aplay
```

### Qwen3-TTS

[Qwen3-TTS](https://github.com/QwenLM/Qwen3-TTS) is a high-quality text-to-speech model that supports three modes: custom voice (predefined speakers), voice design (natural language instructions), and voice cloning (from reference audio).

#### Setup

Install the `qwen-tts` model in the Model gallery or run `local-ai models install qwen-tts`.

#### C++ / GGML gallery variants

For a native backend, install one of the Base variants `qwen3-tts-cpp`, `qwen3-tts-cpp-0.6b-base-q4`, `qwen3-tts-cpp-1.7b-base`, or `qwen3-tts-cpp-1.7b-base-q4`. These variants accept saved Voice Library profiles and are advertised automatically. Gallery entries containing `customvoice` or `voicedesign` provide their respective Qwen modes but are intentionally excluded from raw reference-audio cloning.

A private Qwen C++ Base conversion with an opaque filename can declare the capability explicitly. The tokenizer GGUF can sit beside the talker GGUF for automatic discovery:

```yaml
name: company-narrator-engine
backend: qwen3-tts-cpp
parameters:
  model: qwen-private/talker.gguf
known_usecases:
  - tts
tts:
  voice_cloning: true
  audio_path: voices/default-reference.wav  # optional fallback
```

#### Usage

Use the tts endpoint by specifying the qwen-tts backend:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "qwen-tts",
     "input":"Hello world, this is a test."
   }' | aplay
```

#### Language

You can hint the synthesis language with the `language` request field:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{
     "model": "qwen-tts",
     "input": "Bonjour le monde.",
     "language": "fr"
   }' | aplay
```

Supported languages: `en` (English), `zh` (Chinese), `ru` (Russian), `ja` (Japanese), `ko` (Korean), `de` (German), `fr` (French), `es` (Spanish), `it` (Italian), `pt` (Portuguese).

The value is matched case-insensitively and accepts a few forms for convenience:

- the two-letter code (`fr`, `FR`)
- a locale/region form, whose region is ignored (`fr-FR`, `pt_BR`, `zh-Hans` → `fr`/`pt`/`zh`)
- the English full name (`french`, `Portuguese`)

If the field is omitted or the value isn't one of the supported languages, the backend defaults to English.

#### Custom Voice Mode

Qwen3-TTS supports predefined speakers. You can specify a speaker using the `voice` parameter:

```yaml
name: qwen-tts
backend: qwen-tts
parameters:
  model: Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice
tts:
  voice: "Vivian"  # Available speakers: Vivian, Serena, Uncle_Fu, Dylan, Eric, Ryan, Aiden, Ono_Anna, Sohee
```

Available speakers:
- **Chinese**: Vivian, Serena, Uncle_Fu, Dylan, Eric
- **English**: Ryan, Aiden
- **Japanese**: Ono_Anna
- **Korean**: Sohee

#### Voice Design Mode

Voice Design allows you to create custom voices using natural language instructions. Configure the model with an `instruct` option:

```yaml
name: qwen-tts-design
backend: qwen-tts
parameters:
  model: Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign
options:
  - "instruct:体现撒娇稚嫩的萝莉女声，音调偏高且起伏明显，营造出黏人、做作又刻意卖萌的听觉效果。"
```

Then use the model:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "qwen-tts-design",
     "input":"Hello world, this is a test."
   }' | aplay
```

#### Per-request instructions

Instead of (or in addition to) the static YAML `instruct` option, you can pass an
`instructions` string per request. It maps to the OpenAI
[`instructions`](https://platform.openai.com/docs/api-reference/audio/createSpeech) field
and takes precedence over the YAML option when set, falling back to it when empty. This lets
a single model config serve a different emotion (CustomVoice) or a different designed voice
(VoiceDesign) on every request - useful for roleplay/narration clients that need many voices:

```
curl http://localhost:8080/v1/audio/speech -H "Content-Type: application/json" -d '{
     "model": "qwen-tts-design",
     "input": "Hello world, this is a test.",
     "instructions": "A calm, low-pitched elderly storyteller with a warm tone."
   }' | aplay
```

Backends that do not support style/voice instructions simply ignore the field.

You can also pass backend-specific generation parameters per request via the LocalAI
`params` extension (a string-to-string map; values are coerced to the backend's expected
types). For example, with the Chatterbox backend:

```
curl http://localhost:8080/v1/audio/speech -H "Content-Type: application/json" -d '{
     "model": "chatterbox",
     "input": "Hello world, this is a test.",
     "params": { "exaggeration": "0.7", "cfg_weight": "0.3", "temperature": "0.8" }
   }' | aplay
```

#### Voice Clone Mode

Voice Clone allows you to clone a voice from reference audio. Configure the model with an `AudioPath` and optional `ref_text`:

```yaml
name: qwen-tts-clone
backend: qwen-tts
parameters:
  model: Qwen/Qwen3-TTS-12Hz-1.7B-Base
tts:
  voice_cloning: true  # optional for this Base model; useful when a private checkpoint has an opaque name
  audio_path: "path/to/reference_audio.wav"  # Reference audio file
options:
  - "ref_text:This is the transcript of the reference audio."
  - "x_vector_only_mode:false"  # Set to true to use only speaker embedding (ref_text not required)
```

You can also use URLs or base64 strings for the reference audio. The backend automatically detects the mode based on available parameters (AudioPath → VoiceClone, instruct option → VoiceDesign, voice parameter → CustomVoice).

Then use the model:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "qwen-tts-clone",
     "input":"Hello world, this is a test."
   }' | aplay
```

#### Multi-Voice Clone Mode

Qwen3-TTS also supports loading multiple voices for voice cloning, allowing you to select different voices at request time. Configure multiple voices using the `voices` option:

```yaml
name: qwen-tts-multi-voice
backend: qwen-tts
parameters:
  model: Qwen/Qwen3-TTS-12Hz-1.7B-Base
options:
  - voices:[{"name":"jane","audio":"voices/jane.wav","ref_text":"voices/jane-ref.txt"},{"name":"john","audio":"voices/john.wav","ref_text":"voices/john-ref.txt"}]
```

The `voices` option accepts a JSON array where each voice entry must have:
- `name`: The voice identifier (used in API requests)
- `audio`: Path to the reference audio file (relative to model directory or absolute)
- `ref_text`: Path to the reference text file for the audio it is paired with

Then use the model with voice selection:

```bash
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "qwen-tts-multi-voice",
     "input":"Hello world, this is Jane speaking.",
     "voice": "jane"
   }' | aplay

# Switch to a different voice
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "qwen-tts-multi-voice",
     "input":"Hello world, this is John speaking.",
     "voice": "john"
   }' | aplay
```

**Voice Selection Priority:**
1. `voice` parameter in the API request (highest priority)
2. `voice` option in the model configuration
3. Error if voice is not found among configured voices

**Error Handling:**
If you request a voice that doesn't exist in the voices list, the API will return an error with a list of available voices:
```json
{"error": "Voice 'unknown' not found. Available voices: jane, john"}
```

**Backward Compatibility:**
The multi-voice mode is backward compatible with existing single-voice configurations. Models using `audio_path` in the `tts` section will continue to work as before.

You can also use a `config-file` to specify TTS models and their parameters.

In the following example, a custom config loads `xtts_v2` with a default cloning reference and language.

```yaml
name: xtts_v2
backend: coqui
parameters:
  language: fr
  model: tts_models/multilingual/multi-dataset/xtts_v2

tts:
  voice_cloning: true
  audio_path: voices/reference.wav
```

For XTTS/YourTTS, `tts.audio_path` is the default cloning reference and a saved Voice Library profile overrides it per request. Other Coqui model families are not advertised as Voice Library-compatible unless they match the supported variant rules or are explicitly verified with `tts.voice_cloning: true`.

With this config, you can now use the following curl command to generate a text-to-speech audio file:
```bash
curl -L http://localhost:8080/tts \
    -H "Content-Type: application/json" \
    -d '{
"model": "xtts_v2",
"input": "Bonjour, je suis Ana Florence. Comment puis-je vous aider?"
}' | aplay
```

## Response format

To provide some compatibility with OpenAI API regarding `response_format`, ffmpeg must be installed (or a docker image including ffmpeg used) to leverage converting the generated wav file before the api provide its response.

Warning regarding a change in behaviour. Before this addition, the parameter was ignored and a wav file was always returned, with potential codec errors later in the integration (like trying to decode a mp3 file from a wav, which is the default format used by OpenAI)

Supported format thanks to ffmpeg are `wav`, `mp3`, `aac`, `flac`, `opus`, defaulting to `wav` if an unknown or no format is provided.

```bash
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{
  "input": "Hello world",
  "model": "tts",
  "response_format": "mp3"
}'
```

If a `response_format` is added in the query (other than `wav`) and ffmpeg is not available, the call will fail.
