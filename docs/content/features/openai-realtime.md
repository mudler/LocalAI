
---
title: "Realtime API"
weight: 60
---

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

## Transports

The Realtime API supports two transports: **WebSocket** and **WebRTC**.

### WebSocket

Connect to the WebSocket endpoint:

```
ws://localhost:8080/v1/realtime?model=gpt-realtime
```

Audio is sent and received as raw PCM in the WebSocket messages, following the OpenAI Realtime API protocol.

### WebRTC

The WebRTC transport enables browser-based voice conversations with lower latency. Connect by POSTing an SDP offer to the REST endpoint:

```
POST http://localhost:8080/v1/realtime?model=gpt-realtime
Content-Type: application/sdp

<SDP offer body>
```

The response contains the SDP answer to complete the WebRTC handshake.

#### Opus backend requirement

WebRTC uses the Opus audio codec for encoding and decoding audio on RTP tracks. The **opus** backend must be installed for WebRTC to work. Install it from the model gallery:

```bash
curl http://localhost:8080/models/apply -H "Content-Type: application/json" -d '{"id": "opus"}'
```

Or set the `EXTERNAL_GRPC_BACKENDS` environment variable if running a local build:

```bash
EXTERNAL_GRPC_BACKENDS=opus:/path/to/backend/go/opus/opus
```

The opus backend is loaded automatically when a WebRTC session starts. It does not require any model configuration file — just the backend binary.

## Protocol

The API follows the OpenAI Realtime API protocol for handling sessions, audio buffers, and conversation items.
