+++
disableToc = false
title = "Speaker Diarization"
weight = 17
url = "/features/audio-diarization/"
+++

Speaker diarization answers the question **"who spoke when?"** — given an audio clip with multiple speakers, it returns time-stamped segments labelled with a stable speaker ID (`SPEAKER_00`, `SPEAKER_01`, …).

LocalAI exposes this through the `/v1/audio/diarization` endpoint, modelled after `/v1/audio/transcriptions`. Two backends are supported today:

- **[sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx)** — pyannote-3.0 segmentation + a speaker-embedding extractor (3D-Speaker, NeMo, WeSpeaker) + fast clustering. Pure diarization — no transcription cost. Recommended when you only need speaker turns.
- **[vibevoice.cpp](https://github.com/microsoft/VibeVoice)** — produces speaker-labelled segments as a by-product of its long-form ASR pass, so you can optionally get a transcript per segment for free.

Because diarization is exposed as a regular OpenAI-compatible endpoint, any HTTP client works. There is no Python dependency on pyannote or NeMo on the consumer side.

## Endpoint

```
POST /v1/audio/diarization
Content-Type: multipart/form-data
```

| Field | Type | Description |
|-------|------|-------------|
| `file` | file (required) | audio file in any format `ffmpeg` accepts |
| `model` | string (required) | name of the diarization-capable model |
| `num_speakers` | int | exact speaker count when known (>0 forces; 0 = auto) |
| `min_speakers` | int | hint when auto-detecting |
| `max_speakers` | int | hint when auto-detecting |
| `clustering_threshold` | float | cosine distance threshold used when `num_speakers` is unknown |
| `min_duration_on` | float | discard segments shorter than this many seconds |
| `min_duration_off` | float | merge gaps shorter than this many seconds |
| `language` | string | only meaningful for backends that bundle ASR (e.g. vibevoice) |
| `include_text` | bool | when the backend can emit per-segment transcript for free, populate it |
| `response_format` | string | `json` (default), `verbose_json`, or `rttm` |

### Response — `json` (default)

Compact payload, no transcription, no per-speaker summary:

```json
{
  "task": "diarize",
  "duration": 12.34,
  "num_speakers": 2,
  "segments": [
    {"id": 0, "speaker": "SPEAKER_00", "label": "0", "start": 0.00, "end": 2.34},
    {"id": 1, "speaker": "SPEAKER_01", "label": "1", "start": 2.34, "end": 4.10}
  ]
}
```

`speaker` is the normalized, zero-padded label clients should display. `label` preserves the raw backend-emitted ID for clients that maintain their own speaker dictionary.

### Response — `verbose_json`

Adds per-speaker totals and (when the backend supports it and `include_text=true`) the per-segment transcript:

```json
{
  "task": "diarize",
  "duration": 12.34,
  "language": "en",
  "num_speakers": 2,
  "segments": [
    {"id": 0, "speaker": "SPEAKER_00", "label": "0", "start": 0.00, "end": 2.34, "text": "Hello, world."},
    {"id": 1, "speaker": "SPEAKER_01", "label": "1", "start": 2.34, "end": 4.10, "text": "How are you?"}
  ],
  "speakers": [
    {"id": "SPEAKER_00", "label": "0", "total_speech_duration": 5.6, "segment_count": 3},
    {"id": "SPEAKER_01", "label": "1", "total_speech_duration": 1.76, "segment_count": 1}
  ]
}
```

### Response — `rttm`

NIST RTTM, the standard interchange format used by `pyannote.metrics` / `dscore`:

```
SPEAKER audio 1 0.000 2.340 <NA> <NA> SPEAKER_00 <NA> <NA>
SPEAKER audio 1 2.340 1.760 <NA> <NA> SPEAKER_01 <NA> <NA>
```

Returned as `Content-Type: text/plain; charset=utf-8`.

## Quick start

```bash
curl http://localhost:8080/v1/audio/diarization \
  -H "Content-Type: multipart/form-data" \
  -F file="@meeting.wav" \
  -F model="pyannote-diarization" \
  -F num_speakers=3
```

## Backend setup — sherpa-onnx (pure diarization)

Sherpa-onnx needs two ONNX models: pyannote segmentation and a speaker-embedding extractor. Place them under your LocalAI models directory and reference them from the YAML:

```yaml
name: pyannote-diarization
backend: sherpa-onnx
type: diarization
parameters:
  model: sherpa-onnx-pyannote-segmentation-3-0/model.onnx
options:
  - diarize.embedding_model=3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx
  # Optional clustering knobs (per-call DiarizeRequest fields override these):
  - diarize.threshold=0.5
  - diarize.min_duration_on=0.3
  - diarize.min_duration_off=0.5
known_usecases:
  - FLAG_DIARIZATION
```

Both `model:` and `diarize.embedding_model=` are resolved relative to the LocalAI models directory.

## Backend setup — vibevoice.cpp (diarization + ASR)

vibevoice.cpp's ASR mode emits `[{Start, End, Speaker, Content}]` natively, so a single pass gives both diarization and transcription:

```yaml
name: vibevoice-diarize
backend: vibevoice-cpp
parameters:
  model: vibevoice-asr.gguf
options:
  - type=asr
  - tokenizer=vibevoice-tokenizer.gguf
known_usecases:
  - FLAG_DIARIZATION
  - FLAG_TRANSCRIPT
```

Pass `include_text=true` on the request to populate the `text` field on each diarization segment.

```bash
curl http://localhost:8080/v1/audio/diarization \
  -H "Content-Type: multipart/form-data" \
  -F file="@interview.wav" \
  -F model="vibevoice-diarize" \
  -F include_text=true \
  -F response_format=verbose_json
```

## Notes

- **Speaker identity across files**: speaker IDs (`SPEAKER_00`, `SPEAKER_01`, …) are local to each request. To track the same person across multiple recordings, combine `/v1/audio/diarization` with `/v1/voice/embed` (speaker embedding) and maintain your own embedding store.
- **Hints vs. forces**: `num_speakers` overrides clustering when set; `min_speakers` / `max_speakers` are advisory and only honored by backends that expose a range hint. vibevoice.cpp ignores them — its model picks the count itself.
- **Sample rate**: input is automatically converted to 16 kHz mono via ffmpeg before the backend sees it; sherpa-onnx pyannote-3.0 requires 16 kHz.
