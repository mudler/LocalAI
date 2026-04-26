+++
disableToc = false
title = "Voice Recognition"
weight = 15
url = "/features/voice-recognition/"
+++

LocalAI supports voice (speaker) recognition through the
`speaker-recognition` backend: speaker verification (1:1), speaker
identification (1:N) against a built-in vector store, speaker
embedding, and demographic analysis (age / gender / emotion from
voice).

The audio analog to [Face Recognition](/features/face-recognition/),
following the same two-engine pattern under one image.

## Engines

| Gallery entry | Model | Size | License |
|---|---|---|---|
| `speechbrain-ecapa-tdnn` | ECAPA-TDNN on VoxCeleb (SpeechBrain) | ~17 MB | **Apache 2.0 — commercial-safe** |
| `wespeaker-resnet34` | WeSpeaker ResNet34 ONNX | ~26 MB | **Apache 2.0 — commercial-safe** |

Both entries are commercial-safe Apache-2.0. SpeechBrain is the
default — it's a lightweight pure-PyTorch checkpoint that auto-
downloads on first use. The `wespeaker-resnet34` entry wires the
direct-ONNX path for CPU-only deployments that don't want the torch
runtime.

## Quickstart

Install the default backend and model:

```bash
local-ai models install speechbrain-ecapa-tdnn
```

Verify that two audio clips were spoken by the same person:

```bash
curl -sX POST http://localhost:8080/v1/voice/verify \
  -H "Content-Type: application/json" \
  -d '{
    "model": "speechbrain-ecapa-tdnn",
    "audio1": "https://example.com/alice_1.wav",
    "audio2": "https://example.com/alice_2.wav"
  }'
```

Response:

```json
{
  "verified": true,
  "distance": 0.18,
  "threshold": 0.25,
  "confidence": 28.0,
  "model": "speechbrain-ecapa-tdnn",
  "processing_time_ms": 340.0
}
```

## 1:N identification workflow (register → identify → forget)

Same flow as face recognition, same in-memory vector store under the
hood.

1. Register known speakers:

    ```bash
    curl -sX POST http://localhost:8080/v1/voice/register \
      -H "Content-Type: application/json" \
      -d '{
        "model": "speechbrain-ecapa-tdnn",
        "name": "Alice",
        "audio": "https://example.com/alice.wav"
      }'
    # → {"id": "b2f...", "name": "Alice", "registered_at": "2026-04-22T..."}
    ```

2. Identify an unknown probe:

    ```bash
    curl -sX POST http://localhost:8080/v1/voice/identify \
      -H "Content-Type: application/json" \
      -d '{
        "model": "speechbrain-ecapa-tdnn",
        "audio": "https://example.com/unknown.wav",
        "top_k": 5
      }'
    # → {"matches": [{"id":"b2f...","name":"Alice","distance":0.19,"match":true,...}]}
    ```

3. Remove a speaker by ID:

    ```bash
    curl -sX POST http://localhost:8080/v1/voice/forget \
      -d '{"id": "b2f..."}'
    # → 204 No Content
    ```

{{% notice warning %}}
**Storage caveat.** The default vector store is in-memory. All
registered speakers are lost when LocalAI restarts. Persistent storage
(pgvector) is a tracked future enhancement shared with face
recognition — the voice-recognition HTTP API is designed to swap the
backing store without changing the wire format.
{{% /notice %}}

## API reference

### `POST /v1/voice/verify` (1:1)

| field | type | description |
|---|---|---|
| `model` | string | gallery entry name (e.g. `speechbrain-ecapa-tdnn`) |
| `audio1`, `audio2` | string | URL, base64, or data-URI of an audio file |
| `threshold` | float, optional | cosine-distance cutoff; default 0.25 for ECAPA-TDNN |
| `anti_spoofing` | bool, optional | reserved — unused in the current release |

Returns `verified`, `distance`, `threshold`, `confidence`, `model`,
and `processing_time_ms`.

### `POST /v1/voice/analyze`

Returns demographic attributes (age, gender, emotion) inferred from
speech:

| field | type | description |
|---|---|---|
| `model` | string | gallery entry |
| `audio` | string | URL / base64 / data-URI |
| `actions` | string[] | subset of `["age","gender","emotion"]`; empty = all supported |

Emotion is inferred from the SUPERB emotion-recognition checkpoint
(`superb/wav2vec2-base-superb-er`, Apache 2.0) — 4-way categorical
neutral / happy / angry / sad. The model auto-downloads on the first
analyze call.

Age and gender are **opt-in**: no standard-transformers checkpoint
with a clean classifier head is shipped as the default. The
high-accuracy Audeering age/gender model uses a custom multi-task
head that `AutoModelForAudioClassification` doesn't load safely
(the age weights are silently dropped and the classifier is
re-initialised with random values). To enable age/gender, set
`age_gender_model:<repo>` in the model YAML's `options:` pointing at
a checkpoint with a vanilla `Wav2Vec2ForSequenceClassification`
head. Override the emotion default similarly via `emotion_model:`.
Set either to an empty string to disable that head.

If a head fails to load (offline, disk full, `transformers`
missing), the engine degrades gracefully: it still returns the
attributes it could compute. When nothing can be computed the backend
returns `501 Unimplemented`.

Analyze is supported by both `speechbrain-ecapa-tdnn` and
`wespeaker-resnet34` — the speaker recognizer and the analysis head
are independent.

### `POST /v1/voice/register` (1:N enrollment)

| field | type | description |
|---|---|---|
| `model` | string | voice recognition model |
| `audio` | string | speaker audio to enroll |
| `name` | string | human-readable label |
| `labels` | map[string]string, optional | arbitrary metadata |
| `store` | string, optional | vector store model; defaults to local-store |

Returns `{id, name, registered_at}`. The `id` is an opaque UUID used
by `/v1/voice/identify` and `/v1/voice/forget`.

### `POST /v1/voice/identify` (1:N recognition)

| field | type | description |
|---|---|---|
| `model` | string | voice recognition model |
| `audio` | string | probe audio |
| `top_k` | int, optional | max matches to return; default 5 |
| `threshold` | float, optional | cosine-distance cutoff; default 0.25 |
| `store` | string, optional | vector store model |

Returns a list of matches sorted by ascending distance, each with
`id`, `name`, `labels`, `distance`, `confidence`, and `match`
(`distance ≤ threshold`).

### `POST /v1/voice/forget`

| field | type | description |
|---|---|---|
| `id` | string | ID returned by `/v1/voice/register` |

Returns `204 No Content` on success, `404 Not Found` if the ID is
unknown.

### `POST /v1/voice/embed`

Returns the L2-normalized speaker embedding vector.

| field | type | description |
|---|---|---|
| `model` | string | voice model |
| `audio` | string | URL / base64 / data-URI |

Returns `{embedding: float[], dim: int, model: string}`. Dimension
depends on the recognizer: 192 for ECAPA-TDNN, 256 for WeSpeaker
ResNet34.

> **Note:** the OpenAI-compatible `/v1/embeddings` endpoint is
> intentionally text-only — it does nothing useful with audio input.
> Use `/v1/voice/embed` for audio.

## Audio input

Audio is materialised by the HTTP layer to a temporary WAV file
before the gRPC call. All audio fields accept:

- `http://` / `https://` URLs (downloaded server-side, subject to
  `ValidateExternalURL` safety checks).
- Raw base64 (no prefix).
- Data URIs (`data:audio/wav;base64,...`).

The backend itself always receives a filesystem path — the same
convention the Whisper / Voxtral transcription backends use.

## Threshold reference

| Recognizer | Cosine-distance threshold |
|---|---|
| ECAPA-TDNN (SpeechBrain, VoxCeleb) | ~0.25 |
| WeSpeaker ResNet34 | ~0.30 |
| 3D-Speaker ERes2Net | ~0.28 |

Pass `threshold` explicitly when switching recognizers — the per-model
default only applies when omitted.

## Related features

- [Face Recognition](/features/face-recognition/) — the image analog;
  the two share a registry design.
- [Audio to Text](/features/audio-to-text/) — transcription (Whisper,
  Voxtral, faster-whisper). Runs in addition to, not instead of,
  voice recognition.
- [Stores](/features/stores/) — the generic vector store powering
  both the face and voice 1:N recognition pipelines.
- [Embeddings](/features/embeddings/) — text-only OpenAI-compatible
  embedding endpoint; for audio embeddings use `/v1/voice/embed`.
