+++
disableToc = false
title = "Voice Recognition"
weight = 36
url = "/features/voice-recognition/"
+++

![Voice recognition: register, identify, and forget voiceprints in a vector store, for 1:1 verify or 1:N identify](/images/diagrams/voice-recognition-flow.png)

LocalAI supports voice (speaker) recognition: speaker verification
(1:1), speaker identification (1:N) against a built-in vector store,
speaker embedding, and demographic analysis (age / gender / emotion
from voice).

The audio analog to [Face Recognition](/features/face-recognition/),
served over the same `/v1/voice/*` HTTP API by two backends:

- **`voice-detect` (recommended, default).** A standalone C++/ggml
  engine ([voice-detect.cpp](https://github.com/localai-org/voice-detect.cpp)):
  no Python, no onnxruntime, no torch runtime. Each gallery entry is a
  single self-describing GGUF. This is the recommended option for new
  deployments.
- **`speaker-recognition` (Python).** The original SpeechBrain / ONNX
  backend. Still supported; see [the Python backend](#speaker-recognition-python-backend)
  below.

Both backends expose the identical wire format, so the API examples on
this page work with either - only the gallery entry name (the `model`
field) changes.

## voice-detect (ggml) backend

The `voice-detect` backend reads the embedding (or analysis)
architecture (`voicedetect.arch`) directly from the GGUF metadata, so
installing a gallery entry is all that is needed to select an engine. It
drives the VoiceEmbed / VoiceVerify / VoiceAnalyze gRPC rpcs behind the
`/v1/voice/{embed,verify,analyze,register,identify,forget}` endpoints.

### Gallery entries

| Gallery entry | Model | Embedding dim | License |
|---|---|---|---|
| `voice-detect-ecapa-tdnn` | SpeechBrain ECAPA-TDNN (VoxCeleb) | 192 | **Apache 2.0 - commercial-safe** |
| `voice-detect-wespeaker-resnet34` | WeSpeaker ResNet34 (VoxCeleb) | 256 | CC-BY-4.0 |
| `voice-detect-eres2net` | 3D-Speaker ERes2Net (VoxCeleb) | 192 | **Apache 2.0 - commercial-safe** |
| `voice-detect-campplus` | 3D-Speaker CAM++ (VoxCeleb) | 192 | **Apache 2.0 - commercial-safe** |
| `voice-detect-emotion-wav2vec2` | audEERING wav2vec2 (age / gender / emotion) | analyze head | **CC-BY-NC-SA-4.0 - non-commercial** |

The four speaker-recognition entries drive verify / embed / identify.
`voice-detect-emotion-wav2vec2` is the analysis head behind
`/v1/voice/analyze` (continuous age estimate plus gender and emotion
class scores) and is **non-commercial / research use only**.

### Quickstart

Install the default entry (recommended for copy-paste):

```bash
local-ai models install voice-detect-ecapa-tdnn
```

Verify that two audio clips were spoken by the same person:

```bash
curl -sX POST http://localhost:8080/v1/voice/verify \
  -H "Content-Type: application/json" \
  -d '{
    "model": "voice-detect-ecapa-tdnn",
    "audio1": "https://example.com/alice_1.wav",
    "audio2": "https://example.com/alice_2.wav"
  }'
```

Analyze age / gender / emotion (install the analyze entry first):

```bash
local-ai models install voice-detect-emotion-wav2vec2

curl -sX POST http://localhost:8080/v1/voice/analyze \
  -H "Content-Type: application/json" \
  -d '{"model": "voice-detect-emotion-wav2vec2", "audio": "https://example.com/alice.wav"}'
```

The 1:N register / identify / forget workflow and the rest of the API
are identical to the [API reference](#api-reference) below - just pass a
`voice-detect-*` model name. The default verify threshold is ~0.25 for
the ECAPA-TDNN / ERes2Net / CAM++ recognizers and ~0.30 for WeSpeaker
ResNet34.

## speaker-recognition (Python) backend

The `speaker-recognition` backend follows the same two-engine pattern
under one image.

### Engines

| Gallery entry | Model | Size | License |
|---|---|---|---|
| `speechbrain-ecapa-tdnn` | ECAPA-TDNN on VoxCeleb (SpeechBrain) | ~17 MB | **Apache 2.0 - commercial-safe** |
| `wespeaker-resnet34` | WeSpeaker ResNet34 ONNX | ~26 MB | **Apache 2.0 - commercial-safe** |

Both entries are commercial-safe Apache-2.0. SpeechBrain is the
default - it's a lightweight pure-PyTorch checkpoint that auto-
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
recognition - the voice-recognition HTTP API is designed to swap the
backing store without changing the wire format.
{{% /notice %}}

## Naming speakers in diarization and live transcription

The parakeet-cpp backend can put the names of registered voices on
diarization results and on live transcription speaker segments. Without
this, speakers only carry labels such as `SPEAKER_00`.

1. Register each voice with the WeSpeaker encoder. Install the model with
   `local-ai models install voice-detect-wespeaker-resnet34`, then call
   `/v1/voice/register` with `"model": "voice-detect-wespeaker-resnet34"`
   (see the [1:N workflow](#1n-identification-workflow-register--identify--forget)).
2. Install one of the gallery models that loads the same encoder:
   `parakeet-cpp-nemotron-3-diarization-speakers` (diarization),
   `parakeet-cpp-nemotron-3-diarization-asr-speakers` (diarization with
   `include_text`) or `parakeet-cpp-realtime-scene-speakers` (live
   transcription). Each one adds
   `speaker_model:voice-detect-wespeaker-resnet34.gguf` to a
   parakeet-cpp model config.
3. Call `/v1/audio/diarization` with that model. Matched segments gain a
   `name` and a `name_score`, and the matching entry in `speakers` gains a
   `name`. `speaker` stays `SPEAKER_NN`, and RTTM output is unchanged. See
   [Speaker Diarization]({{% relref "audio-diarization" %}}) for the
   response.

### Which voices are used

LocalAI sends the backend only the registered voices made by the same
encoder as the model's `speaker_model:` file. Each registered voice is
tagged with the name of the voice-detect model that made it, which by
default is the GGUF file name (`voice-detect-wespeaker-resnet34.gguf` for the
gallery entry). The tag must equal the base name of the `speaker_model:`
file. Voices made with another encoder are ignored, and LocalAI logs a
warning when that leaves no usable voice. Voices registered before the tag
existed have no tag: they are used when their embedding size matches the
tagged ones (or all of them, when no voice carries a matching tag). The
backend skips a voice whose embedding size does not match the speaker model's,
with a warning in the LocalAI log. Naming then falls back to the remaining
voices, or to no names.

{{% notice warning %}}
Do not set a `model_name:` option on the voice-detect model config. It
replaces the default name, the voices are then tagged with it, and they no
longer match the `speaker_model:` file. Keep the default name.
{{% /notice %}}

### Options

These go in the `options:` list of the parakeet-cpp model config (see
[Audio to Text]({{% relref "audio-to-text" %}}) for the other parakeet-cpp
options).

| Option | Default | Meaning |
|---|---|---|
| `speaker_model:<path>` | none | speaker encoder GGUF; needs a diarization model (the primary one, or `diarization_model:`) |
| `speaker_threshold:<float>` | `0.5` | largest distance (1 minus cosine similarity, the unit `/v1/voice/identify` reports) at which a speaker is named; must be in (0, 2) |
| `speaker_margin:<float>` | `0.05` | the best match must beat the runner-up by this much, otherwise the speaker stays unnamed; must be in [0, 1) |

parakeet.cpp's measured starting values for `speaker_threshold` are 0.5 for
WeSpeaker ResNet34 and CAM++, and 0.3 for ECAPA. A lower value names fewer
speakers and makes fewer mistakes.

### Limits

- The voice registry is in memory and global. Registered names disappear when
  LocalAI restarts, and every user of the instance shares them.
- Anyone who is allowed to call a model with `speaker_model:` can learn which
  registered names match their audio, and their audio is matched against voices
  registered by any user, because the voice registry is global. Restrict such
  models with the per-user model allowlist.
- With `include_text=true` the names use the default threshold and margin:
  `speaker_threshold` and `speaker_margin` only apply to diarization without
  text.
- In live transcription, a speaker segment that closes before its speaker
  is identified has no name. Later segments of that speaker do.
- Overlapping speech is not resolved.
- Accuracy was measured on one fixture (two read-speech voices). Check the
  threshold on your own audio.
- The backend needs a libparakeet with C-API v10. With an older library a
  model config that sets `speaker_model:` fails to load.

## API reference

### `POST /v1/voice/verify` (1:1)

| field | type | description |
|---|---|---|
| `model` | string | gallery entry name (e.g. `speechbrain-ecapa-tdnn`) |
| `audio1`, `audio2` | string | URL, base64, or data-URI of an audio file |
| `threshold` | float, optional | cosine-distance cutoff; default 0.25 for ECAPA-TDNN |
| `anti_spoofing` | bool, optional | reserved - unused in the current release |

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
(`superb/wav2vec2-base-superb-er`, Apache 2.0) - 4-way categorical
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
`wespeaker-resnet34` - the speaker recognizer and the analysis head
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
> intentionally text-only - it does nothing useful with audio input.
> Use `/v1/voice/embed` for audio.

## Audio input

Audio is materialised by the HTTP layer to a temporary WAV file
before the gRPC call. All audio fields accept:

- `http://` / `https://` URLs (downloaded server-side, subject to
  `ValidateExternalURL` safety checks).
- Raw base64 (no prefix).
- Data URIs (`data:audio/wav;base64,...`).

The backend itself always receives a filesystem path - the same
convention the Whisper / Voxtral transcription backends use.

## Threshold reference

| Recognizer | Cosine-distance threshold |
|---|---|
| ECAPA-TDNN (SpeechBrain, VoxCeleb) | ~0.25 |
| WeSpeaker ResNet34 | ~0.30 |
| 3D-Speaker ERes2Net | ~0.28 |

Pass `threshold` explicitly when switching recognizers - the per-model
default only applies when omitted.

## Related features

- [Face Recognition](/features/face-recognition/) - the image analog;
  the two share a registry design.
- [Audio to Text](/features/audio-to-text/) - transcription (Whisper,
  Voxtral, faster-whisper). Runs in addition to, not instead of,
  voice recognition.
- [Stores](/features/stores/) - the generic vector store powering
  both the face and voice 1:N recognition pipelines.
- [Embeddings](/features/embeddings/) - text-only OpenAI-compatible
  embedding endpoint; for audio embeddings use `/v1/voice/embed`.

## Portable profile registration

`POST /v1/voice/register` also accepts a JSON alternative to `audio`:

```javascript
// result is the parsed diarization response; slot is a selected raw speaker slot.
const request = {
  model: "parakeet-diarization",
  name: "Ada",
  labels: {team: "research"},
  speaker_slot: slot,
  speaker_profiles: result.speaker_profiles
};
// POST JSON.stringify(request) with Content-Type: application/json.
```

Copy the complete `speaker_profiles` object returned by diarization unchanged.
Select `speaker_slot` explicitly, including for slot zero. It is the raw numeric
slot whose decimal string matches the diarization `label`, not a normalized
`SPEAKER_NN`, array index, or display name. `audio` and `speaker_profiles` are
mutually exclusive. `speaker_slot` without profiles is also invalid. Audio-only
registration keeps its existing JSON shape and behavior.

The server loads the requested, authorized model and obtains encoder identity and
dimension from backend metadata. It validates the complete profile export and
selects the requested usable slot. Missing slots, unavailable speech, unsupported
versions, non-finite/zero/wrong-size vectors and encoder mismatch return 400.
A backend without trusted encoder metadata returns 501. Success returns the
existing `{id, name, registered_at}` response.

Portable registrations store the **server-derived SHA-256 identity**, not a
caller-provided filename tag. Offline/live recognition admits these registrations
only when the loaded encoder has the same identity and dimension. Legacy audio
registrations retain their filename-tag compatibility rules. `/v1/voice/identify`
filters incompatible matches; a backend unable to report trusted identity cannot
match portable registrations, even when vector dimensions agree. Filtering can
return fewer than `top_k` results. The parakeet diarization model need not support
the separate audio-only VoiceEmbed RPC used by `/v1/voice/identify`.

Each successful enrollment inserts a new registration with its own ID and vector.
Duplicate display names do not merge embeddings or update an earlier enrollment.
There is no automatic enrollment or sample aggregation.

The recognition registry is **global, in-memory and per LocalAI instance**;
registrations are lost on restart and are not synchronized across frontends.
This is not durable “remembering” and not a per-user private address book. The
persistent `/api/voice-profiles` TTS-cloning feature is unrelated. Export and
registration use the existing voice-recognition permission, with existing model
access restrictions; permission does not establish biometric consent.

API tracing excludes the entire exchange for `/v1/audio/diarization`, its
`/audio/diarization` alias, and `/v1/voice/register` before capturing bodies.
This also protects JSON base64 audio when profile export is off. These routes
produce no in-memory or persisted API trace; other routes keep their existing
tracing behavior. External proxies and client logs must apply the same privacy
policy. Existing trace files from older versions are not retroactively scrubbed.

For offline and live diarization replay, registry tags never determine the
encoder dimension. LocalAI orders candidates by registration ID (tagged first),
then uses loaded encoder metadata to filter dimensions. Portable registrations
require an exact SHA-256 identity match as well. Older backends without trusted
metadata reject portable candidates and retain their native legacy dimension
checks. Identification filters compatibility after the store's `top_k` query;
incompatible results can crowd out compatible candidates within that window.
