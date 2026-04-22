+++
disableToc = false
title = "Face Recognition"
weight = 14
url = "/features/face-recognition/"
+++

LocalAI supports face recognition through the `insightface` backend:
face verification (1:1), face identification (1:N) against a built-in
vector store, face embedding, face detection, and demographic analysis
(age / gender).

The backend ships **two interchangeable engines** under one image, each
paired with a distinct gallery entry so users can pick by license and
accuracy needs.

## Licensing — read this first

| Gallery entry | Detector + recognizer | Size | License |
|---|---|---|---|
| `insightface-buffalo-l` | SCRFD-10GF + ArcFace R50 + GenderAge | ~326 MB | **Non-commercial research only** (upstream insightface weights) |
| `insightface-buffalo-s` | SCRFD-500MF + MBF + GenderAge | ~159 MB | **Non-commercial research only** |
| `insightface-opencv` | YuNet + SFace | ~40 MB | **Apache 2.0 — commercial-safe** |

The `insightface` Python library itself is MIT, but the pretrained model
packs (buffalo_l, buffalo_s, antelopev2) are released by the upstream
maintainers for **non-commercial research use only**. Pick the
`insightface-opencv` entry for production / commercial deployments.

## Quickstart

Pull the commercial-safe backend (recommended for copy-paste):

```bash
local-ai models install insightface-opencv
```

Verify that two images depict the same person:

```bash
curl -sX POST http://localhost:8080/v1/face/verify \
  -H "Content-Type: application/json" \
  -d '{
    "model": "insightface-opencv",
    "img1": "https://example.com/alice_1.jpg",
    "img2": "https://example.com/alice_2.jpg"
  }'
```

Response:

```json
{
  "verified": true,
  "distance": 0.27,
  "threshold": 0.35,
  "confidence": 23.1,
  "model": "insightface-opencv",
  "img1_area": { "x": 120.4, "y": 82.1, "w": 198.3, "h": 260.5 },
  "img2_area": { "x": 110.8, "y": 95.0, "w": 205.6, "h": 268.2 },
  "processing_time_ms": 412.0
}
```

## 1:N identification workflow (register → identify → forget)

This is the primary "face recognition" flow. Under the hood it uses
LocalAI's built-in in-memory vector store — no external database to
stand up.

1. Register known faces:

    ```bash
    curl -sX POST http://localhost:8080/v1/face/register \
      -H "Content-Type: application/json" \
      -d '{
        "model": "insightface-buffalo-l",
        "name": "Alice",
        "img": "https://example.com/alice.jpg"
      }'
    # → {"id": "8b7...", "name": "Alice", "registered_at": "2026-04-21T..."}
    ```

2. Identify an unknown probe:

    ```bash
    curl -sX POST http://localhost:8080/v1/face/identify \
      -H "Content-Type: application/json" \
      -d '{
        "model": "insightface-buffalo-l",
        "img": "https://example.com/unknown.jpg",
        "top_k": 5
      }'
    # → {"matches": [{"id":"8b7...","name":"Alice","distance":0.22,"match":true,...}]}
    ```

3. Remove a person by ID:

    ```bash
    curl -sX POST http://localhost:8080/v1/face/forget \
      -d '{"id": "8b7..."}'
    # → 204 No Content
    ```

{{% alert icon="⚠️" color="warning" %}}
**Storage caveat.** The default vector store is in-memory. All
registered faces are lost when LocalAI restarts. Persistent storage
(pgvector) is a tracked future enhancement — the face-recognition HTTP
API is designed to swap the backing store without changing the wire
format.
{{% /alert %}}

## API reference

### `POST /v1/face/verify` (1:1)

| field | type | description |
|---|---|---|
| `model` | string | gallery entry name (e.g. `insightface-buffalo-l`) |
| `img1`, `img2` | string | URL, base64, or data-URI |
| `threshold` | float, optional | cosine-distance cutoff; default depends on engine |
| `anti_spoofing` | bool, optional | reserved — unused in the current release |

Returns `verified`, `distance`, `threshold`, `confidence`, `model`,
`img1_area`, `img2_area`, and `processing_time_ms`.

### `POST /v1/face/analyze`

Returns demographic attributes for every detected face:

| field | type | description |
|---|---|---|
| `model` | string | gallery entry |
| `img` | string | URL / base64 / data-URI |
| `actions` | string[] | subset of `["age","gender","emotion","race"]`; empty = all supported |

Only `insightface-buffalo-l` / `insightface-buffalo-s` populate age and
gender (genderage head). `insightface-opencv` returns face regions with
empty attributes — SFace has no demographic classifier. Emotion and
race are always empty in the current release.

### `POST /v1/face/register` (1:N enrollment)

| field | type | description |
|---|---|---|
| `model` | string | face recognition model |
| `img` | string | face to enroll |
| `name` | string | human-readable label |
| `labels` | map[string]string, optional | arbitrary metadata |
| `store` | string, optional | vector store model; defaults to local-store |

Returns `{id, name, registered_at}`. The `id` is an opaque UUID used by
`/v1/face/identify` and `/v1/face/forget`.

### `POST /v1/face/identify` (1:N recognition)

| field | type | description |
|---|---|---|
| `model` | string | face recognition model |
| `img` | string | probe image |
| `top_k` | int, optional | max matches to return; default 5 |
| `threshold` | float, optional | cosine-distance cutoff; default 0.35 (ArcFace) |
| `store` | string, optional | vector store model; defaults to local-store |

Returns a list of matches sorted by ascending distance, each with `id`,
`name`, `labels`, `distance`, `confidence`, and `match`
(`distance ≤ threshold`).

### `POST /v1/face/forget`

| field | type | description |
|---|---|---|
| `id` | string | ID returned by `/v1/face/register` |

Returns `204 No Content` on success, `404 Not Found` if the ID is
unknown.

### `POST /v1/face/embed`

Returns the L2-normalized face embedding vector for the detected face.

| field | type | description |
|---|---|---|
| `model` | string | face model |
| `img` | string | URL / base64 / data-URI |

Returns `{embedding: float[], dim: int, model: string}`. Dimension is
512 for the insightface ArcFace/MBF recognizers and 128 for OpenCV's
SFace.

> **Note:** the OpenAI-compatible `/v1/embeddings` endpoint is
> intentionally text-only by contract (`input` is a string or list of
> strings of TEXT to embed) — passing an image data-URI there does
> nothing useful. Use `/v1/face/embed` for image inputs.

### Reused endpoint

- `POST /v1/detection` — returns face bounding boxes with
  `class_name: "face"`; works for both engines.

## Choosing an engine

| Need | Entry |
|---|---|
| Commercial product | `insightface-opencv` |
| Highest accuracy (research / demos) | `insightface-buffalo-l` |
| Edge / low-memory / research | `insightface-buffalo-s` |

The recommended default `threshold` for `/v1/face/verify` and
`/v1/face/identify` depends on the recognizer:

| Recognizer | Cosine-distance threshold |
|---|---|
| ArcFace R50 (`buffalo_l`) | ~0.35 |
| MBF (`buffalo_s`) | ~0.40 |
| SFace (`opencv`) | ~0.50 |

Pass `threshold` explicitly when switching engines — the per-engine
default only fires when the field is omitted.

## Related features

- [Object Detection](/features/object-detection/) — generic bounding-box
  detection; `/v1/detection` works with the insightface backend too.
- [Embeddings](/features/embeddings/) — raw vector extraction; face
  embeddings live in the same endpoint under the hood.
- [Stores](/features/stores/) — the generic vector store powering the
  1:N recognition pipeline.
