# Realtime speech-to-speech E2E

`realtime_s2s_test.go` drives `/v1/realtime` with one any-to-any model and no
chained pipeline. A fake client speaks audio that a TTS generated, streams it
as `input_audio_buffer.append` chunks, and checks session setup, server VAD and
manual-commit turns, audio deltas, transcripts, barge-in and a tool-call round
trip.

## Mock variant (CI)

Uses the mock backend. No weights. This is what CI runs.

```sh
make test-realtime
```

## Real-weights variant (manual, GPU recommended)

Runs the same specs against LFM3-Audio on the llama-cpp backend. It is never
run in CI: the label filter of `make test-realtime` excludes `real-models`.

The client renders its input speech with a sherpa-onnx VITS voice. A separate
sherpa-onnx ASR transcribes the reply audio, so the checks do not rely on the
model's own transcript. Assertions are robust to sampling: reply duration and
loudness, keywords found in the transcript or the ASR text, and event order.

```sh
# Build the backend images once (pick the build type for the host).
BUILD_TYPE=cublas CUDA_MAJOR_VERSION=13 make docker-build-llama-cpp
make docker-build-sherpa-onnx

# Run. Downloads about 3 GB on the first run; KEEP_WORK keeps it for the next.
KEEP_WORK=1 WORK_DIR=$HOME/lfm3-e2e LFM3_GPU_LAYERS=99 make test-realtime-s2s-real-nobuild
```

`make test-realtime-s2s-real` also builds the two images. Set `LFM3_VARIANT=q8`
to test `lfm3-audio-2b-q8` instead of `lfm3-audio-2b`.

To run against a different setup, set the variables yourself and call ginkgo:

| Variable | Meaning |
|---|---|
| `REALTIME_S2S_MODEL` | Model name in the models path. Selects the real variant. |
| `REALTIME_S2S_TTS` | TTS model that renders the input utterances. |
| `REALTIME_S2S_ASR` | ASR model that transcribes the reply audio. Optional. |
| `REALTIME_MODELS_PATH`, `REALTIME_BACKENDS_PATH` | Models and extracted backends. |

The gRPC-level check for the same backend is `make test-extra-backend-llama-cpp-lfm3`
(TTS, TTS stream, ASR and the `s2s` capability).
