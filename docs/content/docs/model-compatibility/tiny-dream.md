
+++
disableToc = false
title = "Tiny dream"
weight = 4
+++


[Tiny dream](https://github.com/symisc/tiny-dream#tiny-dreaman-embedded-header-only-stable-diffusion-inference-c-librarypixlabiotiny-dream) allows to generate images with stable diffusion by using CPU.

## Setup

This is an extra backend - in the container is already available and there is nothing to do for the setup.


## Model setup

There is nothing to be done for the model setup. You can already start to use bark. The models will be downloaded the first time you use the backend.

## Usage

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