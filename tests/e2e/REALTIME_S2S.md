# Realtime speech-to-speech E2E

`realtime_s2s_test.go` drives `/v1/realtime` with the four shapes of the LFM3
gallery entries: an any-to-any model or a chained pipeline, each with a VAD or
without one. A fake client speaks audio that a TTS generated, streams it as
`input_audio_buffer.append` chunks, and checks session setup, the turn
detection the model allows, server VAD and manual-commit turns, audio deltas,
transcripts, barge-in and a tool-call round trip.

| Shape | Mock model | Gallery entry |
|---|---|---|
| any-to-any, VAD | `realtime-s2s-mock` | `lfm3-audio-2b-vad`, `-q8-vad` |
| any-to-any, no VAD | `realtime-s2s-mock-novad` | `lfm3-audio-2b`, `-q8` |
| pipeline, VAD | `realtime-pipeline` | `lfm3-audio-2b-pipeline-vad`, `-q8-pipeline-vad` |
| pipeline, no VAD | `realtime-pipeline-novad` | `lfm3-audio-2b-pipeline`, `-q8-pipeline` |

Specs that need a VAD (server VAD turn, barge-in) run only for the VAD shapes.
Barge-in runs for the chained mock pipeline in the real variant only, because
the mock LLM answers at once. A VAD-less shape also checks that it starts with
turn detection off and that `server_vad` is refused.

## Mock variant (CI)

Uses the mock backend. No weights. This is what CI runs.

```sh
make test-realtime
```

## Real-weights variant (manual, GPU recommended)

Runs the same specs against LFM3-Audio on the llama-cpp backend, once for each
shape in the table above, with configs that mirror the gallery entries (the VAD
is the sherpa silero model its extracted backend already has, in place of
`silero-vad-ggml`). It is never run in CI: the label filter of
`make test-realtime` excludes `real-models`.

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
to test the Q8_0 entries instead of the Q4_K_M ones. Set `LFM3_MATRIX` to run
some shapes only, as `kind:vad` words, for example:

```sh
LFM3_MATRIX="pipeline:novad pipeline:vad" make test-realtime-s2s-real-nobuild
```

The default runs `any-to-any:vad any-to-any:novad pipeline:vad pipeline:novad`.

To run against a different setup, set the variables yourself and call ginkgo:

| Variable | Meaning |
|---|---|
| `REALTIME_S2S_MODEL` | Model name in the models path. Selects the real variant. |
| `REALTIME_S2S_KIND` | `any-to-any` (default) or `pipeline`. |
| `REALTIME_S2S_VAD` | `1` (default) when the model has a VAD, `0` when it has none. |
| `REALTIME_S2S_TTS` | TTS model that renders the input utterances. |
| `REALTIME_S2S_ASR` | ASR model that transcribes the reply audio. Optional. |
| `REALTIME_MODELS_PATH`, `REALTIME_BACKENDS_PATH` | Models and extracted backends. |

The gRPC-level check for the same backend is `make test-extra-backend-llama-cpp-lfm3`
(TTS, TTS stream, ASR and the `s2s` capability).
