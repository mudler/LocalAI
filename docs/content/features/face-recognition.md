+++
disableToc = false
title = "Face Recognition"
weight = 14
url = "/features/face-recognition/"
+++

![Face recognition: 1:N match against a vector store, with an anti-spoofing liveness gate that can veto a verification](/images/diagrams/face-recognition-flow.png)

LocalAI supports face recognition: face verification (1:1), face
identification (1:N) against a built-in vector store, face embedding,
face detection, demographic analysis (age / gender), and antispoofing /
liveness detection.

The same `/v1/face/*` HTTP API is served by two backends:

- **`face-detect` (recommended, default).** A standalone C++/ggml
  engine ([face-detect.cpp](https://github.com/mudler/face-detect.cpp)):
  no Python, no onnxruntime, no torch runtime. Each gallery entry is a
  single self-describing GGUF. This is the recommended option for new
  deployments.
- **`insightface` (Python).** The original ONNX Runtime backend. Still
  supported; see [the Python backend](#insightface-python-backend) below.

Both backends expose the identical wire format, so the API examples in
this page work with either - only the gallery entry name (the `model`
field) changes.

## face-detect (ggml) backend

The `face-detect` backend reads the detector and recognizer architecture
(`facedetect.arch`) directly from the GGUF metadata, so installing a
gallery entry is all that is needed to select an engine. It drives the
Embeddings / Detect / FaceVerify / FaceAnalyze gRPC rpcs behind the
`/v1/face/{embed,verify,analyze,detect,register,identify,forget}`
endpoints.

### Licensing - read this first

| Gallery entry | Detector + recognizer | Embedding dim | License |
|---|---|---|---|
| `face-detect-buffalo-l` | SCRFD-10GF + ArcFace R50 + GenderAge | 512 | **Non-commercial research only** (upstream insightface weights) |
| `face-detect-buffalo-m` | SCRFD-2.5GF + ArcFace R50 + GenderAge | 512 | **Non-commercial research only** |
| `face-detect-buffalo-s` | SCRFD-500MF + MBF + GenderAge | 512 | **Non-commercial research only** |
| `face-detect-yunet-sface` | YuNet + SFace (OpenCV Zoo) | 128 | **Apache 2.0 - commercial-safe** |

The insightface buffalo packs (buffalo_l / buffalo_m / buffalo_s) are
released by the upstream maintainers for **non-commercial research use
only**. Pick the `face-detect-yunet-sface` entry for production /
commercial deployments.

### Quickstart

Install the commercial-safe entry (recommended for copy-paste):

```bash
local-ai models install face-detect-yunet-sface
```

Verify that two images depict the same person:

```bash
curl -sX POST http://localhost:8080/v1/face/verify \
  -H "Content-Type: application/json" \
  -d '{
    "model": "face-detect-yunet-sface",
    "img1": "https://example.com/alice_1.jpg",
    "img2": "https://example.com/alice_2.jpg"
  }'
```

Detect faces and analyze demographics (buffalo entries populate
age / gender; YuNet + SFace returns regions only):

```bash
curl -sX POST http://localhost:8080/v1/face/detect \
  -H "Content-Type: application/json" \
  -d '{"model": "face-detect-buffalo-l", "img": "https://example.com/group.jpg"}'

curl -sX POST http://localhost:8080/v1/face/analyze \
  -H "Content-Type: application/json" \
  -d '{"model": "face-detect-buffalo-l", "img": "https://example.com/alice.jpg"}'
```

The 1:N register / identify / forget workflow and the rest of the API
are identical to the [API reference](#api-reference) below - just pass a
`face-detect-*` model name. The per-engine verify thresholds are ~0.35
for the buffalo ArcFace/MBF recognizers and ~0.363 for SFace.

## insightface (Python) backend

The `insightface` backend ships **two interchangeable engines** under
one image, each paired with a distinct gallery entry so users can pick
by license and accuracy needs.

### Licensing - read this first

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

{{% notice warning %}}
**Storage caveat.** The default vector store is in-memory. All
registered faces are lost when LocalAI restarts. Persistent storage
(pgvector) is a tracked future enhancement — the face-recognition HTTP
API is designed to swap the backing store without changing the wire
format.
{{% /notice %}}

## API reference

### `POST /v1/face/verify` (1:1)

| field | type | description |
|---|---|---|
| `model` | string | gallery entry name (e.g. `insightface-buffalo-l`) |
| `img1`, `img2` | string | URL, base64, or data-URI |
| `threshold` | float, optional | cosine-distance cutoff; default depends on engine |
| `anti_spoofing` | bool, optional | also run MiniFASNet liveness on each image — see [Antispoofing](#antispoofing-liveness-detection) |

Returns `verified`, `distance`, `threshold`, `confidence`, `model`,
`img1_area`, `img2_area`, and `processing_time_ms`. When
`anti_spoofing` is set, the response also carries per-image liveness
fields: `img1_is_real`, `img1_antispoof_score`, `img2_is_real`,
`img2_antispoof_score`. A failed liveness check on either image forces
`verified=false` regardless of similarity.

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

## Antispoofing (liveness detection)

All gallery entries ship the [Silent-Face-Anti-Spoofing](https://github.com/minivision-ai/Silent-Face-Anti-Spoofing)
MiniFASNetV2 + MiniFASNetV1SE ensemble (Apache 2.0, ~4 MB total, CPU-only)
alongside the face recognition weights. Set `anti_spoofing: true` on
`/v1/face/verify` or `/v1/face/analyze` to run liveness on each detected
face. The two models look at different crop scales and their softmax
outputs are averaged before argmax — the upstream-recommended setup.

`/v1/face/verify` with liveness gating:

```bash
curl -sX POST http://localhost:8080/v1/face/verify \
  -H "Content-Type: application/json" \
  -d '{
    "model": "insightface-opencv",
    "img1": "https://example.com/alice_selfie.jpg",
    "img2": "https://example.com/alice_id_scan.jpg",
    "anti_spoofing": true
  }'
```

Response (fields added when `anti_spoofing` is enabled):

```json
{
  "verified": true,
  "distance": 0.27,
  "threshold": 0.5,
  "confidence": 46.0,
  "model": "insightface-opencv",
  "img1_area": { "x": 120, "y": 82, "w": 198, "h": 260 },
  "img2_area": { "x": 110, "y": 95, "w": 205, "h": 268 },
  "img1_is_real": true,
  "img1_antispoof_score": 0.82,
  "img2_is_real": true,
  "img2_antispoof_score": 0.74,
  "processing_time_ms": 431.0
}
```

If either image fails liveness (`is_real=false`), `verified` is forced
to `false` — similarity alone is not enough.

`/v1/face/analyze` reports per-face `is_real` and `antispoof_score`
when the flag is set.

**Fail-loud semantics.** If `anti_spoofing: true` is sent against a
model installed without the MiniFASNet files (e.g. a custom entry that
only listed the face recognition weights), the request returns a gRPC
`FAILED_PRECONDITION` error — the endpoint will never silently return
`is_real=false`. Re-install the gallery entry or point the backend at a
model that bundles the MiniFASNet ONNX files.

{{% notice info %}}
The MiniFASNet score is best at catching **printed photos and screen
replays**. Deepfake videos and high-quality prosthetics are out of
scope — liveness here is a low-cost first line of defence, not a
guarantee. For higher assurance, combine with challenge-response (e.g.
ask the user to turn their head).
{{% /notice %}}

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
