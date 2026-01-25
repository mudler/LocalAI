
+++
disableToc = false
title = "🗣 Text to audio (TTS)"
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

### VibeVoice

[VibeVoice-Realtime](https://github.com/microsoft/VibeVoice) is a real-time text-to-speech model that generates natural-sounding speech with voice cloning capabilities.

#### Setup

Install the `vibevoice` model in the Model gallery or run `local-ai run models install vibevoice`.

#### Usage

Use the tts endpoint by specifying the vibevoice backend:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "vibevoice",
     "input":"Hello!"
   }' | aplay
```

#### Voice cloning

VibeVoice supports voice cloning through voice preset files. You can configure a model with a specific voice:

```yaml
name: vibevoice
backend: vibevoice
parameters:
  model: microsoft/VibeVoice-Realtime-0.5B
tts:
  voice: "Frank"  # or use audio_path to specify a .pt file path
  # Available English voices: Carter, Davis, Emma, Frank, Grace, Mike
```

Then you can use the model:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "vibevoice",
     "input":"Hello!"
   }' | aplay
```

### Pocket TTS

[Pocket TTS](https://github.com/kyutai-labs/pocket-tts) is a lightweight text-to-speech model designed to run efficiently on CPUs. It supports voice cloning through HuggingFace voice URLs or local audio files.

#### Setup

Install the `pocket-tts` model in the Model gallery or run `local-ai run models install pocket-tts`.

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

Install the `qwen-tts` model in the Model gallery or run `local-ai run models install qwen-tts`.

#### Usage

Use the tts endpoint by specifying the qwen-tts backend:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "model": "qwen-tts",
     "input":"Hello world, this is a test."
   }' | aplay
```

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

#### Voice Clone Mode

Voice Clone allows you to clone a voice from reference audio. Configure the model with an `AudioPath` and optional `ref_text`:

```yaml
name: qwen-tts-clone
backend: qwen-tts
parameters:
  model: Qwen/Qwen3-TTS-12Hz-1.7B-Base
tts:
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

## Using config files

You can also use a `config-file` to specify TTS models and their parameters.

In the following example we define a custom config to load the `xtts_v2` model, and specify a voice and language.

```yaml

name: xtts_v2
backend: coqui
parameters:
  language: fr
  model: tts_models/multilingual/multi-dataset/xtts_v2

tts:
  voice: Ana Florence
```

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