# insightface backend (LocalAI)

Face recognition backend backed by ONNX Runtime. Provides face
verification (1:1), face analysis (age/gender), face detection, face
embedding, and — via LocalAI's built-in vector store — 1:N
identification.

## Engines

This backend ships with **two** interchangeable engines selected via
`LoadModel.Options["engine"]`:

| engine | Implementation | Models | License |
|---|---|---|---|
| `insightface` (default) | `insightface.app.FaceAnalysis` | `buffalo_l`, `buffalo_s`, `antelopev2` | **Non-commercial research use only** |
| `onnx_direct` | OpenCV `FaceDetectorYN` + `FaceRecognizerSF` | OpenCV Zoo YuNet + SFace | Apache 2.0 (commercial-safe) |

Both engines implement the same `FaceEngine` protocol in `engines.py`,
so the gRPC servicer in `backend.py` doesn't need to know which one is
active.

## LoadModel options

Common:

| option | default | description |
|---|---|---|
| `engine` | `insightface` | one of `insightface`, `onnx_direct` |
| `det_size` | `640x640` (insightface), `320x320` (onnx_direct) | detector input size |
| `det_thresh` | `0.5` | detector confidence threshold |
| `verify_threshold` | `0.35` | default cosine distance cutoff for FaceVerify |

`insightface` engine:

| option | default | description |
|---|---|---|
| `model_pack` | `buffalo_l` | which insightface pack to load |

`onnx_direct` engine:

| option | default | description |
|---|---|---|
| `detector_onnx` | *(required)* | path to YuNet-compatible ONNX |
| `recognizer_onnx` | *(required)* | path to SFace-compatible ONNX |

## Adding a new model pack

1. If it's an insightface pack (auto-downloadable or manually extracted
   into `~/.insightface/models/<name>/`), just add a new gallery entry
   in `backend/index.yaml` with `options: ["engine:insightface",
   "model_pack:<name>"]`. No code change.
2. If it's an Apache-licensed ONNX pair, add a gallery entry with
   `options: ["engine:onnx_direct", "detector_onnx:...",
   "recognizer_onnx:..."]`. If the detector or recognizer has a
   different input-tensor shape than YuNet/SFace, you may need a new
   engine implementation in `engines.py`; the two-engine seam makes
   that a self-contained change.

## Running tests locally

```bash
make -C backend/python/insightface         # install deps + bake models
make -C backend/python/insightface test    # run test.py
```

The OpenCV Zoo tests skip gracefully when `/models/opencv/*.onnx` is
absent (e.g. on dev boxes where `install.sh` wasn't run).
