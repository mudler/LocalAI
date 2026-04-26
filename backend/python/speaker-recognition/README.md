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
- `POST /v1/voice/analyze` — voice demographics, loaded lazily on
  the first analyze call:
  - **Emotion** (default, opt-out): `superb/wav2vec2-base-superb-er`
    (Apache-2.0), 4-way categorical (neutral / happy / angry / sad).
  - **Age + gender** (opt-in): no default — wire a checkpoint with a
    standard `Wav2Vec2ForSequenceClassification` head via
    `age_gender_model:<repo>` in options. The Audeering
    age-gender model is *not* usable as a drop-in because its
    multi-task head isn't loadable via `AutoModelForAudioClassification`.

  Both heads are optional. When nothing loads, the engine returns 501.

## Audio input

Audio is materialised by the HTTP layer to a temp wav before calling
the gRPC backend. Accepted input forms on the HTTP side: URL, data-URI,
or raw base64. The backend itself always receives a filesystem path.
