+++
disableToc = false
title = "Talking Head"
description = "Talk to a face: type or speak, and a portrait answers in a lip-synced video clip."
weight = 25
keywords = ["talking head", "avatar", "avatarforcing", "lip sync", "video", "add-on"]
+++

Talking Head is a conversation with a face. You pick a portrait, then speak or type. The page writes a reply with a chat model, speaks it with a text-to-speech model, and plays a video clip in which the portrait says the reply.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | The page `/app/addons/talking-head/talking-head`. **Open** on a video model can also lead to it. |
| Needs | An avatar model (a video model with the `avatarforcing-cpp` backend, for example `avatarforcing` from the gallery) and a text-to-speech model. A chat model for **Reply**, and a transcription model for **Hold to talk**. |
| Permissions | The `addons` feature. Each call goes to the core API with your own session, so the page can do only what you can do. |
| Guide | [avatarforcing.cpp talking heads]({{% relref "/models/capabilities/vision/video-generation#avatarforcingcpp-talking-heads" %}}) |

{{% callout style="warning" %}}
The `avatarforcing` weights are licensed under [CC BY-NC 4.0](https://creativecommons.org/licenses/by-nc/4.0/). Use them for non-commercial purposes only.
{{% /callout %}}

## Set up the models

When the avatar model or a speech model is missing, or there is no pipeline model yet, the page offers **Set up talking head**. You can also open it from the settings. It installs what a talking head needs from the gallery and writes a pipeline model that joins the parts:

| Part | Default pick | Other picks |
|---|---|---|
| Avatar | `avatarforcing` (float32 GGUF, about 1.2 GB) | A build of the entry, when the gallery entry lists more than one |
| Chat | `llama-3.2-1b-instruct:q4_k_m` | `gemma-3-1b-it`, `qwen3-0.6b`, or any installed chat model |
| Transcription | `whisper-tiny-en-q5_1` | `whisper-base-en-q5_1`, `whisper-base-q5_1`, or any installed one |
| Speech | `vits-piper-en_US-amy-sherpa` | `kokoro`, or any installed one |
| Voice detection | `silero-vad` | None |

1. Choose **Set up talking head**. The sheet shows each part, whether it is installed, and the size of each download and of all of them.
2. Change a pick if you want another model. A part that is installed already is skipped, so running the setup again only fills the gaps.
3. Read the licence notice of the avatar weights and confirm that you use them for non-commercial purposes only.
4. Choose **Install and set up**. Each part shows its progress. **Cancel** stops the download in progress. If a part fails, the sheet shows the error and **Retry** continues with the parts that are not installed.
5. When the setup is done, the page selects the new pipeline model and the avatar model.

The pipeline model is named `talking-head` (or `talking-head-2` and so on, when the name is taken). A pipeline with the same parts is used again, not written twice. Talk can use it too.

Only an administrator can install models. Anyone else sees the list of models to ask an administrator for.

The setup uses the routes of the web UI's model pages, with your own session: `GET /api/models` (the gallery), `GET /api/models/estimate/:id` (the size), `GET /api/models/variants/:id` (the builds), `POST /api/models/install/:id`, `GET /models/jobs/:uuid` (the progress), `POST /api/operations/:jobID/cancel` and `POST /models/import` (the pipeline model).

To install by hand instead:

```bash
local-ai models install avatarforcing
```

Then install a text-to-speech model, a chat model and a transcription model from the gallery. A small chat model is enough, because the replies are short. If you have a [pipeline model]({{% relref "/api/openai-realtime" %}}) for Talk, the page can use its chat, transcription and speech models.

## Talk to the face

1. Choose **Upload a portrait**. Pick a photo of one face. In the crop sheet, drag the picture or use the arrow keys until the face is in the middle of the square, then choose **Use this portrait**.
2. Do one of these:
   - Hold **Hold to talk**, speak, and release the button. Space and Enter on the button work the same way.
   - Type a message and choose **Reply** (or press Enter).
   - Type a sentence and choose **Say this**. The face says your text as it is, with no chat model.
3. The page shows each stage of the turn and its time: **Transcribe**, **Reply**, **Speech** and **Video**. **Cancel** stops the turn.
4. The clip plays when it is ready. The conversation keeps every turn with its clip. **Play again** plays a clip again, and **Download** saves it as an MP4 file.

Each turn calls these routes:

| Stage | Route | Input | Output |
|---|---|---|---|
| Transcribe | `POST /v1/audio/transcriptions` | your recording | your words |
| Reply | `POST /v1/chat/completions` (streamed) | the instructions, the earlier turns and your words | the reply text |
| Speech | `POST /tts` | the reply text and the voice | a WAV file |
| Video | `POST /video` | the portrait (`start_image`) and the WAV (`audio`) | an MP4 file, 512x512 at 25 FPS, with the speech as its audio track |

## Settings

The settings button (top right) opens the settings. The page keeps them in this browser.

| Setting | Meaning |
|---|---|
| Pipeline | A pipeline model. It sets the chat, transcription and speech models at once. |
| Chat, Transcription, Speech and Avatar model | The model of each stage. Only installed models that can do the stage are listed. |
| Voice | The voice of the speech model. Blank: the model default. |
| Instructions | Sent to the chat model before the conversation. The default asks for one or two short sentences. |
| Quality (nfe) | Sampler steps of the avatar model, from 3 to 12, sent as `params.nfe`. The default is 6. A lower value is faster and less detailed. |
| Seed | The seed of the avatar model. Blank: the model default. |

## Speed

The video stage takes most of the time. On a 16-core x86_64 CPU it needs about 15 seconds for each second of speech, so keep replies short. On a GPU it is much faster. The first turn also waits while LocalAI loads each model.

## What stays where

- The portraits stay in this browser (local storage), up to six. Remove one with the cross on its thumbnail. A portrait goes to the server only as part of a `/video` request.
- The conversation and the clips stay in this tab. They are gone when you close the page.
- LocalAI saves each clip under its generated content path, as for every `/video` request.

## Limits

- Each reply is a new clip that starts from the portrait. There is no live mode yet: the face does not move between replies, and you cannot interrupt a reply.
- The model does not look for a face. Crop the portrait so that one face fills the middle of the square.
- The clip is H.264 video with AAC audio. A browser without these codecs offers the file to download instead of playing it.
