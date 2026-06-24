# Whisper-Medusa Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a dedicated LocalAI speech-to-text backend for aiola Whisper-Medusa checkpoints.

**Architecture:** A Python gRPC backend owns model loading, audio normalization, and Medusa generation. LocalAI's existing `AudioTranscription` RPC remains unchanged; build, gallery, and documentation surfaces follow the existing Python ASR backend pattern.

**Tech Stack:** Python 3.11, PyTorch, torchaudio, transformers, whisper-medusa, gRPC, YAML, Make.

## Global Constraints

- Accept local model paths and Hugging Face model identifiers through `resolve_model_reference`.
- Normalize input audio to mono 16 kHz before generation.
- Default the language to `en` and expose upstream generation regulation options.
- Document upstream's archived status, 30-second clip limit, and checkpoint language limitations.
- Build Linux CPU and NVIDIA CUDA 12 images; do not claim unsupported Darwin or ROCm coverage.

---

### Task 1: Backend behavior

**Files:**
- Create: `backend/python/whisper-medusa/backend.py`
- Create: `backend/python/whisper-medusa/test_unit.py`

**Interfaces:**
- Consumes: LocalAI `LoadModel` and `AudioTranscription` protobuf requests.
- Produces: `BackendServicer`, `_parse_options`, and `_prepare_audio`.

- [ ] Write unit tests for option parsing, mono conversion, resampling, load failure, and transcription.
- [ ] Run `python -m unittest test_unit.py` and confirm it fails because the backend does not exist.
- [ ] Implement the minimal gRPC backend and rerun the unit tests to green.

### Task 2: Packaging and registration

**Files:**
- Create: `backend/python/whisper-medusa/{Makefile,install.sh,protogen.sh,run.sh,test.sh,requirements.txt,requirements-cpu.txt,requirements-cublas12.txt}`
- Modify: `Makefile`
- Modify: `.github/backend-matrix.yml`
- Modify: `backend/index.yaml`

**Interfaces:**
- Consumes: the Python backend Docker build conventions.
- Produces: `whisper-medusa` install/build targets and CPU/CUDA backend images.

- [ ] Add packaging scripts and pinned upstream dependency.
- [ ] Register the backend in Make and backend metadata.
- [ ] Add Linux amd64 CPU and CUDA 12 CI matrix entries.
- [ ] Validate Make and YAML parsing.

### Task 3: Documentation and verification

**Files:**
- Create: `docs/content/features/whisper-medusa.md`
- Modify: `docs/content/features/backends.md`

**Interfaces:**
- Produces: user-facing model YAML and limitation guidance.

- [ ] Document setup, configuration, options, and upstream constraints.
- [ ] Run unit tests, syntax compilation, registration checks, and diff review.
- [ ] Commit with the required `Assisted-by` trailer, push, and open a PR closing issue #3127.
