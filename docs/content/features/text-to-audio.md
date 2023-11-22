
+++
disableToc = false
title = "🗣 Text to audio (TTS)"
weight = 2
+++

The `/tts` endpoint can be used to generate speech from text.

Input: `input`, `model`

For example, to generate an audio file, you can send a POST request to the `/tts` endpoint with the instruction as the request body:

```bash
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{
  "input": "Hello world",
  "model": "tts"
}'
```

Returns an `audio/wav` file.

#### Setup

LocalAI supports [bark]({{%relref "model-compatibility/bark" %}}) , `piper` and `vall-e-x`:

{{% notice note %}}

The `piper` backend is used for `onnx` models and requires the modules to be downloaded first.

To install the `piper` audio models manually:

- Download Voices from https://github.com/rhasspy/piper/releases/tag/v0.0.2
- Extract the `.tar.tgz` files (.onnx,.json) inside `models`
- Run the following command to test the model is working

{{% /notice %}}

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

#### Configuration

Audio models can be configured via `YAML` files. This allows to configure specific setting for each backend. For instance, backends might be specifying a voice or supports voice cloning which must be specified in the configuration file.

```yaml
name: tts
backend: vall-e-x
parameters: ...
```