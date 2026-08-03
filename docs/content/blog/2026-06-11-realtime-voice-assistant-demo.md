+++
title = "Realtime voice assistant demo and pipeline streaming"
date = 2026-06-11
description = "A tiny Go client for the Realtime API with a full talk-back loop and tool calling, plus streaming of the realtime pipeline stages."
url = "/blog/realtime-voice-assistant-demo/"
+++

The new [realtime voice assistant demo](https://github.com/localai-org/localai-realtime-demo) is a small Go client for the Realtime API with a complete talk-back voice loop and tool calling. It is intended as a reference you can read end to end.

On the server side, two supporting changes landed: [streaming of the realtime LLM, TTS and transcription pipeline stages](https://github.com/mudler/LocalAI/pull/10176), and [configurable WebRTC ICE candidates](https://github.com/mudler/LocalAI/pull/10231).

See [Realtime API]({{% relref "features/openai-realtime" %}}).
