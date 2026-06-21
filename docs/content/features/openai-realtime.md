
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
    llm: true             # stream LLM tokens as transcript deltas
    tts: true             # emit audio deltas per synthesized chunk
    transcription: true   # stream transcript text deltas of the user's speech
    clause_chunking: true # synthesize each clause as soon as it completes
```

- **streaming.tts**: emit a `response.output_audio.delta` per audio chunk the TTS backend produces (requires a backend that supports streaming synthesis), instead of one delta for the whole utterance. Falls back to a single unary delta otherwise.
- **streaming.transcription**: stream `conversation.item.input_audio_transcription.delta` events as the transcript is produced (requires a transcription backend that supports streaming).
- **streaming.llm**: stream the LLM reply token-by-token as `response.output_audio_transcript.delta` events. The full reply is buffered and synthesized once it is complete — streamed as audio chunks when `streaming.tts` is enabled (and the TTS backend supports it), otherwise as a single unary delta. Reasoning/thinking is always stripped from the spoken transcript. Tool calls are supported while streaming when the LLM uses its tokenizer template (`use_tokenizer_template: true`): the backend's autoparser then delivers content and tool calls separately, so the spoken transcript never leaks tool-call tokens. Grammar-based function calling keeps the buffered path.
- **streaming.clause_chunking**: instead of buffering the whole reply before TTS, split it into speakable clauses and synthesize each as soon as it completes, lowering the time-to-first-audio. The splitter is script-aware: it uses Unicode sentence segmentation (so it handles CJK `。！？` with no whitespace), CJK clause punctuation (`，、；：`), and Thai/Lao spaces — it does **not** rely on whitespace sentence boundaries, so it works for languages such as Chinese, Japanese and Thai where the old per-sentence approach degraded to whole-message buffering. Requires `streaming.llm`; scripts that genuinely need a dictionary (e.g. Khmer, Burmese) simply stay buffered until a space or end-of-message. Off by default.

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

## Gating a realtime pipeline with voice recognition

A pipeline realtime model can require speaker verification before it responds. Add a `voice_recognition` block under `pipeline`. When present, each committed utterance is verified against authorized speakers; unauthorized utterances are dropped before the LLM runs (no LLM call, no tool execution, no TTS). The session stays open.

The same block also drives two optional, independent behaviors: an authorization gate (`enforce`) and speaker surfacing/personalization (`identity`). Set `enforce: false` to keep recognizing the speaker without ever rejecting a turn.

```yaml
name: my-realtime
pipeline:
  vad: silero-vad
  transcription: whisper
  llm: qwen
  tts: kokoro
  voice_recognition:
    model: speaker-recognition   # the speaker-recognition backend model
    mode: identify               # "identify" (registry) or "verify" (references)
    threshold: 0.25              # cosine distance; <= passes
    enforce: true                # authorization gate (default true)
    when: every                  # "every" (default) or "first"
    on_reject: drop_event        # "drop_event" (default) or "drop_silent"
    anti_spoofing: false         # optional liveness check (verify mode)

    # identify mode: authorized registry identities (multiple persons)
    allow:
      names: ["alice", "bob"]    # match registered speaker names
      labels: ["family"]         # OR any identity carrying this label
      # empty allow = any registered speaker within threshold passes

    # verify mode: reference speakers (multiple persons)
    references:
      - name: alice
        audio: /models/voices/alice.wav
      - name: bob
        audio: /models/voices/bob.wav
```

### Identifying speakers without gating

To recognize who is speaking and surface it to the client and the LLM without ever rejecting a turn, set `enforce: false` and add an `identity` block. The `identity` block works with or without the gate; when it is set, the speaker is resolved on every turn even if `when: first`.

```yaml
name: my-realtime
pipeline:
  vad: silero-vad
  transcription: whisper
  llm: qwen
  tts: kokoro
  voice_recognition:
    model: speaker-recognition
    mode: identify
    threshold: 0.25
    # Authorization gate. Defaults to enforcing (rejects unauthorized speakers).
    # Set enforce:false to identify the speaker WITHOUT rejecting anyone.
    enforce: false
    when: every
    # Surface the recognized speaker to the client and the LLM. Works with or
    # without enforce; when set, identity is resolved on every turn even if
    # when:first.
    identity:
      announce: true            # emit the conversation.item.speaker event
      announce_unknown: false   # also emit it when there is no confident match
      personalize: true         # tell the LLM who is speaking
      inject_name: true         # set the per-message OpenAI name field
      inject_system_note: true  # append a "current speaker" line to the system message
      note_unknown: false       # append a "speaker is unknown" note when unidentified
```

| Field | Meaning |
|-------|---------|
| `model` | Speaker-recognition backend model name. |
| `mode` | `identify` matches against speakers registered via `/v1/voice/register`; `verify` matches against the `references` audios. |
| `threshold` | Maximum cosine distance that still counts as a match (default ~0.25). |
| `enforce` | Authorization gate. `true` (or omitted) rejects unauthorized speakers (the gating behavior above). `false` resolves and surfaces the speaker without ever dropping a turn. |
| `when` | `every` verifies each utterance; `first` verifies once then trusts the session. When an `identity` block is set, the speaker is still resolved on every turn even with `first`. |
| `on_reject` | `drop_event` drops and emits a `speaker_not_authorized` error event; `drop_silent` drops quietly. |
| `anti_spoofing` | Verify mode only: runs the backend liveness check (slower). |
| `allow.names` / `allow.labels` | identify mode: which registry identities are authorized. Empty = any registered speaker. |
| `references` | verify mode: authorized reference speakers; the utterance passes if it matches any. |
| `identity.announce` | Emit the `conversation.item.speaker` event to the client (see below). |
| `identity.announce_unknown` | Also emit that event when there is no confident match. By default the event is emitted only on a match. |
| `identity.personalize` | Inform the LLM who is speaking. |
| `identity.inject_name` | Set the per-message OpenAI `name` field on each user turn. |
| `identity.inject_system_note` | Append a `The current speaker is <Name>.` line to the system message. |
| `identity.note_unknown` | When unidentified, append `The current speaker is unknown.` (lets the model ask who it is talking to). |

`identify` mode requires the voice registry (speakers registered through `/v1/voice/register`). `verify` mode needs no registry: reference audios are embedded once at model load.

### The `conversation.item.speaker` event

When `identity.announce` is enabled, the server emits a `conversation.item.speaker` event after the user conversation item, naming the recognized speaker:

```json
{
  "type": "conversation.item.speaker",
  "item_id": "item_abc",
  "speaker": { "name": "Jeremy", "id": "spk_1", "confidence": 92.0, "distance": 0.1, "matched": true }
}
```

`confidence` is a 0-100 score, `distance` is the cosine distance, and `matched` is `true` when a confident match was found. The `name` and `id` fields are omitted when empty. By default the event is emitted only on a match; set `identity.announce_unknown: true` to also emit it (with `matched: false`) when no speaker is identified.

This event is a LocalAI extension to the OpenAI Realtime API and is server-emitted only. Standard OpenAI Realtime clients ignore event types they do not recognize, so enabling it is non-breaking.

## Examples

- [Realtime voice assistant demo (Go)](https://github.com/localai-org/localai-realtime-demo): a minimal Go client for the Realtime (WebSocket) API with a full talk-back voice loop and an example tool call. Ships a `docker compose` setup that brings up a realtime-capable LocalAI for you.
- [Realtime voice assistant example (Python)](https://github.com/mudler/LocalAI-examples/tree/main/realtime): thin-client architecture (Silero VAD on the client, heavy lifting on LocalAI), suited to running the client on a Raspberry Pi.
