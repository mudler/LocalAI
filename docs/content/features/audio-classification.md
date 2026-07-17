+++
disableToc = false
title = "Sound Classification"
weight = 18
url = "/features/audio-classification/"
+++

Sound-event classification (audio tagging) answers the question **"what am I hearing?"** - given an audio clip, it returns a list of scored [AudioSet](https://research.google.com/audioset/) labels (e.g. *Baby cry, infant cry*, *Glass breaking*, *Dog bark*, *Alarm*).

LocalAI exposes this through the `/v1/audio/classification` endpoint, modelled after `/v1/audio/transcriptions`. The reference backend is **[ced.cpp](https://github.com/mudler/ced.cpp)** (CED, a 527-class AudioSet tagger), a small ViT over a log-mel spectrogram ported to ggml with full PyTorch parity. Apache-2.0 weights are redistributable as GGUF.

Because classification is exposed as a regular OpenAI-style endpoint, any HTTP client works - there is no Python dependency on the consumer side.

## Endpoint

```
POST /v1/audio/classification
Content-Type: multipart/form-data
```

| Field | Type | Description |
|-------|------|-------------|
| `file` | file (required) | audio file in any format `ffmpeg` accepts |
| `model` | string (required) | name of the sound-classification-capable model (e.g. `ced-base-f16`) |
| `top_k` | int | number of top tags to return (0 = backend default) |
| `threshold` | float | drop tags scoring below this value |

### Response

```json
{
  "model": "ced-base-f16",
  "detections": [
    {"index": 23, "label": "Baby cry, infant cry", "score": 0.87},
    {"index": 22, "label": "Crying, sobbing", "score": 0.41}
  ]
}
```

Detections are returned in score-descending order. Scores are per-class probabilities (multi-label, independent), so they do not sum to 1.

## Example

First install a classification model from the gallery (the example below uses `ced-base-f16`):

```bash
local-ai run ced-base-f16
```

```bash
curl http://localhost:8080/v1/audio/classification \
  -H "Content-Type: multipart/form-data" \
  -F file="@/path/to/clip.wav" \
  -F model="ced-base-f16" \
  -F top_k=10
```

## See also

- [Audio to Text]({{% relref "audio-to-text" %}}) - speech transcription
- [Speaker Diarization]({{% relref "audio-diarization" %}}) - who spoke when
