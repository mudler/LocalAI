---
title: "Every modality"
weight: 1
card: "Text, images, video, music, speech and search behind one API."
headline: 'Text, images, video, music and <span class="hl">voice</span>.'
lede: "Send text, image, audio and video requests to one OpenAI-compatible API. Each request runs on the model you installed."
description: "LocalAI runs open models for text, vision, images, video, music, speech, embeddings and reranking behind one OpenAI-compatible API."
docs: "/docs/models/capabilities/"
icon: "modalities"
illus: "modalities"
# demo: a name under static/media/ (<name>.mp4, .webm, .jpg) swaps the illustration for a muted loop.
demo: ""
points:
  - "**Text and vision**: chat, tool calls, image understanding and [object detection](/docs/models/capabilities/vision/object-detection/)."
  - "**Images and video**: [image generation](/docs/models/capabilities/vision/image-generation/) and [video generation](/docs/models/capabilities/vision/video-generation/), also [3D](/docs/models/capabilities/vision/3d-generation/)."
  - "**Animated video** (development build): With the Sandbox add-on and its media profile, the assistant can build short videos with ffmpeg from a sentence: a title card with a fade, or a slideshow with a slow zoom and crossfade, saved as MP4 in your Files. The Sandbox has no internet. Results need a look before you use them. A landscape video can be cropped to vertical. Simple animated charts work with mixed results."
  - "**Music and sound**: make music and sound effects from a text prompt. See [text to audio](/docs/models/capabilities/audio/text-to-audio/)."
  - "**Speech**: text to speech, [transcription](/docs/models/capabilities/audio/audio-to-text/), voice activity detection and speaker recognition. The [Realtime API](/docs/api/openai-realtime/) takes voice in and gives voice back."
  - "**Search**: [embeddings](/docs/models/capabilities/text/embeddings/) and a [reranker](/docs/models/capabilities/text/reranker/)."
  - "Use the OpenAI API with the clients you already have. LocalAI also answers in the Anthropic format, and text to speech and sound generation accept the ElevenLabs format."
  - "The engine runs on a CPU, so it does not need a GPU. NVIDIA, AMD, Intel and Apple GPUs make it faster, and large image and video models need one to be practical."
  - "[Distributed mode](/docs/administration/distributed-mode/) spreads models over several machines."
notes:
  - "Animated video needs the Sandbox add-on, which an admin installs from the development catalog today, and a model that can call tools. A chat uses the media profile unless an admin limits the choice, and the first use pulls an image of about 384 MB."
  - "Each kind of model needs its own backend. LocalAI downloads it the first time a model needs it."
  - "The [compatibility table](/docs/models/compatibility-table/) lists the model families that run."
---
