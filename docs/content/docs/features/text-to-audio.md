
+++
disableToc = false
title = "🗣 Text to audio (TTS)"
weight = 11
url = "/features/text-to-audio/"
+++

The `/tts` endpoint can be used to generate speech from text.

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

### Bark

[Bark](https://github.com/suno-ai/bark) allows to generate audio from text prompts.

This is an extra backend - in the container is already available and there is nothing to do for the setup.

#### Model setup

There is nothing to be done for the model setup. You can already start to use bark. The models will be downloaded the first time you use the backend.

#### Usage

Use the `tts` endpoint by specifying the `bark` backend:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "backend": "bark",
     "input":"Hello!"
   }' | aplay
```

To specify a voice from https://github.com/suno-ai/bark#-voice-presets ( https://suno-ai.notion.site/8b8e8749ed514b0cbf3f699013548683?v=bc67cff786b04b50b3ceb756fd05f68c ), use the `model` parameter:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "backend": "bark",
     "input":"Hello!",
     "model": "v2/en_speaker_4"
   }' | aplay
```

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

### Vall-E-X

[VALL-E-X](https://github.com/Plachtaa/VALL-E-X) is an open source implementation of Microsoft's VALL-E X zero-shot TTS model.

#### Setup

The backend will automatically download the required files in order to run the model.

This is an extra backend - in the container is already available and there is nothing to do for the setup. If you are building manually, you need to install Vall-E-X manually first.

#### Usage

Use the tts endpoint by specifying the vall-e-x backend:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "backend": "vall-e-x",
     "input":"Hello!"
   }' | aplay
```

#### Voice cloning

In order to use voice cloning capabilities you must create a `YAML` configuration file to setup a model:

```yaml
name: cloned-voice
backend: vall-e-x
parameters:
  model: "cloned-voice"
vall-e:
  # The path to the audio file to be cloned
  # relative to the models directory 
  audio_path: "path-to-wav-source.wav"
```

Then you can specify the model name in the requests:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "backend": "vall-e-x",
     "model": "cloned-voice",
     "input":"Hello!"
   }' | aplay
```
