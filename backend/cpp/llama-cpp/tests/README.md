# Native decision bridge validation

The stock dependency is pinned to `a4cb4c61fd9d9c2066c7c1747821d3d65b8943bd`.
`Score(question_type="systemone")` uses upstream decision tasks internally, not
HTTP. Plain Score keeps its existing admission checks. Older dependencies without
`server-decision.cpp` return gRPC `UNIMPLEMENTED` for this request type.

## CPU build

Use a fresh stock checkout at the pin in `backend/cpp/llama-cpp/llama.cpp`.
Do not reuse a customized developer checkout. Apply patches once:

```sh
cd backend/cpp/llama-cpp/llama.cpp
git apply --check ../patches/0001-add-server-task-type-score.patch
git apply ../patches/0001-add-server-task-type-score.patch
git apply --check ../patches/0002-add-server-task-type-tts.patch
git apply ../patches/0002-add-server-task-type-tts.patch
cmake -S . -B build-cpu -DGGML_NATIVE=OFF -DLLAMA_OPENSSL=OFF \
  -DLLAMA_CURL=OFF -DBUILD_SHARED_LIBS=OFF
cmake --build build-cpu --target llama-server -j2
```

For the normal product build, start instead from a fresh unpatched checkout and
run `make -C backend/cpp/llama-cpp grpc-server JOBS=2`; preparation applies the
patches and stages the bridge. This requires CMake packages for gRPC, protobuf,
and Abseil, plus `protoc` and `grpc_cpp_plugin`.

`build-decision-bridge.sh` is an alternative link validation for distro packages
that lack `ProtobufConfig.cmake`. It uses the already patched CPU static libraries
and the bridge source staged by `prepare.sh`. Do not apply patches twice when
staging that source. Set `CPU_BUILD` to the absolute `build-cpu` directory,
`OUT_DIR` to a scratch output directory, and optionally `DEPS_ROOT` to the root
of **locally extracted** distro packages. It does not install or download anything.
It also generates Python bindings, requiring `grpc_python_plugin`.

## Direct RPC smoke

The upstream test fixture `ggml-org/tinylaya-for-testing-gguf` has file
`tinylaya-for-testing-Q8_0.gguf`, size **97,200,288 bytes**, SHA-256
`a8b2b8f7fe6b7e10a884c55bf72362b0a8701e40dc3f332831d58246e5fa0b70`.
This is a test model, not a production gallery recommendation.

Start the backend with cores disabled, using the matching library path when
validating extracted distro dependencies:

```sh
ulimit -c 0
export LD_LIBRARY_PATH="$DEPS_ROOT/usr/lib/x86_64-linux-gnu${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
"$OUT_DIR/grpc-server" --addr 127.0.0.1:50051
```

In another terminal (Python requires `grpcio` and `protobuf`):

```sh
PYTHONPATH="$OUT_DIR" python3 backend/cpp/llama-cpp/tests/decision_smoke.py \
  --address 127.0.0.1:50051 --model "$MODEL_FILE"
```

The smoke covers multiquestion choice/score/noul, normalized probabilities,
positive input and explicit zero output usage, concurrent calls, invalid JSON,
client cancellation/recovery, ordinary Score disabled/enabled, and missing
metadata. It reloads models; do not share the backend with another test runner.
The missing-metadata test makes a temporary equal-length metadata-key rename of
the fixture and explicitly enables embeddings, as appropriate for this encoder.

Limits: immediate client cancellation does not prove interruption during active
evaluation. The tiny fixture finishes too quickly for a deterministic timing-only
assertion; a queue barrier or server-side instrumentation is needed for that gate.
TTS is compiled and linked, not runtime-tested by this text-only fixture. Older
pin compile validation does not establish every supported fork's full build.
Image/projector runtime support is intentionally unavailable in this bridge.

The metadata-stripped encoder with embeddings disabled and `-np 1` aborts
in warmup at `llama-context.cpp`'s output-budget assertion on **clean unpatched**
upstream at the pinned revision as well. This is a preexisting invalid-fixture
configuration hazard, not a decision-dispatch or Score/TTS patch regression.
Do not use that configuration as the missing-metadata test.
