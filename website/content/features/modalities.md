---
title: "Every modality"
weight: 1
card: "Text, images, video, music, speech and search behind one API."
headline: 'Text, images, video, music and <span class="hl">voice</span>.'
lede: "LocalAI is an open-source AI engine and platform. It serves many kinds of open model behind one OpenAI-compatible API, on your own hardware."
description: "LocalAI runs open models for text, vision, images, video, music, speech, embeddings and reranking behind one OpenAI-compatible API."
docs: "/docs/models/capabilities/"
icon: "modalities"
illus: "modalities"
# demo: a name under static/media/ (<name>.mp4, .webm, .jpg) swaps the illustration for a muted loop.
demo: ""
points:
  - "**Text and vision**: chat, tool calls, image understanding and [object detection](/docs/models/capabilities/vision/object-detection/)."
  - "**Images and video**: [image generation](/docs/models/capabilities/vision/image-generation/) and [video generation](/docs/models/capabilities/vision/video-generation/), also [3D](/docs/models/capabilities/vision/3d-generation/)."
  - "**Music and sound**: make music and sound effects from a text prompt. See [text to audio](/docs/models/capabilities/audio/text-to-audio/)."
  - "**Speech**: text to speech, [transcription](/docs/models/capabilities/audio/audio-to-text/), voice activity detection and speaker recognition. The [Realtime API](/docs/api/openai-realtime/) takes voice in and gives voice back."
  - "**Search**: [embeddings](/docs/models/capabilities/text/embeddings/) and a [reranker](/docs/models/capabilities/text/reranker/)."
  - "Use the OpenAI API with the clients you already have. LocalAI also answers in the Anthropic format, and text to speech and sound generation accept the ElevenLabs format."
  - "It runs on a CPU, so no GPU is required. NVIDIA, AMD, Intel and Apple GPUs make it faster."
  - "Run it on a laptop, a server, in a container or on Kubernetes. [Distributed mode](/docs/administration/distributed-mode/) spreads models over several machines."
notes:
  - "Each model kind needs a backend that serves it. LocalAI downloads the backend the first time a model needs it. See the [compatibility table](/docs/models/compatibility-table/)."
  - "LocalAI runs open models. It does not run every model."
---
