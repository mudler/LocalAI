
---
title: "Realtime API"
weight: 60
---

# Realtime API

LocalAI supports the [OpenAI Realtime API](https://platform.openai.com/docs/guides/realtime) which enables low-latency, multi-modal conversations (voice and text) over WebSocket.

To use the Realtime API, you need to configure a pipeline model that defines the components for Voice Activity Detection (VAD), Transcription (STT), Language Model (LLM), and Text-to-Speech (TTS).

## Configuration

Create a model configuration file (e.g., `gpt-realtime.yaml`) in your models directory. For a complete reference of configuration options, see [Model Configuration]({{%relref "advanced/model-configuration" %}}).

```yaml
name: gpt-realtime
pipeline:
  vad: silero-vad-ggml
  transcription: whisper-large-turbo
  llm: qwen3-4b
  tts: tts-1
```

This configuration links the following components:
- **vad**: The Voice Activity Detection model (e.g., `silero-vad-ggml`) to detect when the user is speaking.
- **transcription**: The Speech-to-Text model (e.g., `whisper-large-turbo`) to transcribe user audio.
- **llm**: The Large Language Model (e.g., `qwen3-4b`) to generate responses.
- **tts**: The Text-to-Speech model (e.g., `tts-1`) to synthesize the audio response.

Make sure all referenced models (`silero-vad-ggml`, `whisper-large-turbo`, `qwen3-4b`, `tts-1`) are also installed or defined in your LocalAI instance.

## Usage

Once configured, you can connect to the Realtime API endpoint via WebSocket:

```
ws://localhost:8080/v1/realtime?model=gpt-realtime
```

The API follows the OpenAI Realtime API protocol for handling sessions, audio buffers, and conversation items.
