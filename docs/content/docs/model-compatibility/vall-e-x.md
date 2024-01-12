
+++
disableToc = false
title = "Vall-E-X"
weight = 4
+++

[VALL-E-X](https://github.com/Plachtaa/VALL-E-X) is an open source implementation of Microsoft's VALL-E X zero-shot TTS model.

## Setup

The backend will automatically download the required files in order to run the model.

This is an extra backend - in the container is already available and there is nothing to do for the setup. If you are building manually, you need to install Vall-E-X manually first.

## Usage

Use the tts endpoint by specifying the vall-e-x backend:

```
curl http://localhost:8080/tts -H "Content-Type: application/json" -d '{         
     "backend": "vall-e-x",
     "input":"Hello!"
   }' | aplay
```

## Voice cloning

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
