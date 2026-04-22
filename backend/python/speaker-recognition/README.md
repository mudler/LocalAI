# speaker-recognition

Speaker (voice) recognition backend for LocalAI. The audio analog to
`insightface` — produces speaker embeddings and supports 1:1 voice
verification and voice demographic analysis.

## Engines

- **SpeechBrainEngine** (default): ECAPA-TDNN trained on VoxCeleb.
  192-d L2-normalised embeddings, cosine distance for verification.
  Auto-downloads from HuggingFace on first LoadModel.
- **OnnxDirectEngine**: Any pre-exported ONNX speaker encoder
  (WeSpeaker ResNet, 3D-Speaker ERes2Net, CAM++, …). Model path comes
  from the gallery `files:` entry.

Engine selection is gallery-driven: if the model config provides
`model_path:` / `onnx:` the ONNX engine is used, otherwise the
SpeechBrain engine.

## Endpoints

- `POST /v1/voice/verify` — 1:1 same-speaker check.
- `POST /v1/voice/embed` — extract a speaker embedding vector.
- `POST /v1/voice/analyze` — age / gender / emotion from voice,
  powered by two open-licence HuggingFace checkpoints loaded on the
  first analyze call:
  - `audeering/wav2vec2-large-robust-24-ft-age-gender` (Apache-2.0) —
    age regression + 3-way gender (female / male / child).
  - `superb/wav2vec2-base-superb-er` (Apache-2.0) — 4-way categorical
    emotion (neutral / happy / angry / sad).

  Override either with the `age_gender_model` / `emotion_model` option,
  or set either to the empty string to disable that head. Both are
  optional: if loading fails (network, disk, missing `transformers`)
  the engine raises NotImplemented cleanly so the gRPC layer returns a
  501 rather than a 500.

## Audio input

Audio is materialised by the HTTP layer to a temp wav before calling
the gRPC backend. Accepted input forms on the HTTP side: URL, data-URI,
or raw base64. The backend itself always receives a filesystem path.
