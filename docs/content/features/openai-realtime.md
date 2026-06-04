
---
title: "Realtime API"
weight: 60
---

![The realtime voice loop: VAD to STT to LLM to TTS, over WebSocket or WebRTC](/images/diagrams/realtime-pipeline.png)

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

### Streaming the pipeline

By default each stage runs to completion before the next begins: the whole utterance is transcribed, the full LLM reply is generated, then it is synthesized. Each stage can instead be streamed incrementally, which lowers the time-to-first-audio of a turn:

```yaml
name: gpt-realtime
pipeline:
  vad: silero-vad-ggml
  transcription: whisper-large-turbo
  llm: qwen3-4b
  tts: tts-1
  streaming:
    llm: true            # stream LLM tokens as transcript deltas
    tts: true            # emit audio deltas per synthesized chunk
    transcription: true  # stream transcript text deltas of the user's speech
```

- **streaming.tts**: emit a `response.output_audio.delta` per audio chunk the TTS backend produces, instead of one delta for the whole utterance.
- **streaming.transcription**: stream `conversation.item.input_audio_transcription.delta` events as the transcript is produced (requires a transcription backend that supports streaming).
- **streaming.llm**: stream the LLM reply token-by-token as `response.output_audio_transcript.delta` events and, when `streaming.tts` is also enabled, synthesize each completed sentence as soon as it is ready — overlapping generation, synthesis and playback. Streaming is used only for turns that cannot produce a tool call; turns with tools fall back to the buffered path so partial tool-call output is never spoken.

All streaming flags are off by default, so existing pipelines are unaffected.

### Disabling thinking

For reasoning models, you can force the pipeline LLM's thinking off without editing the LLM model config:

```yaml
pipeline:
  llm: qwen3-4b
  disable_thinking: true   # maps to enable_thinking=false for the realtime LLM
```

This is applied only to the realtime session's copy of the LLM config, so it does not affect other users of the same model. Leave it unset to use the LLM model config's own reasoning settings.

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

#### WebRTC behind Docker host networking or NAT

By default pion gathers a host ICE candidate for every local interface. Under
Docker **host networking** that includes bridge addresses (`docker0`/`veth`,
`172.x`) that a remote browser cannot route to: the call typically connects on a
good candidate and then drops a few seconds later when ICE consent checks fail on
the unreachable ones. Two settings let you advertise only the reachable address:

```bash
# Advertise these IPs as the host ICE candidates (e.g. the host's LAN IP)
LOCALAI_WEBRTC_NAT_1TO1_IPS=192.168.1.10

# ...or restrict ICE gathering to specific interfaces
LOCALAI_WEBRTC_ICE_INTERFACES=eth0
```

{{% notice tip %}}
For a browser on another LAN machine talking to LocalAI in a host-networked
container, set `LOCALAI_WEBRTC_NAT_1TO1_IPS` to the host's LAN IP. This is the
most reliable fix for WebRTC connections that establish and then drop.
{{% /notice %}}

## Protocol

The API follows the OpenAI Realtime API protocol for handling sessions, audio buffers, and conversation items.
