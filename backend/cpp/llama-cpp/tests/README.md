# Native decision bridge validation

The stock dependency is pinned to `bed0a856606ee4a24a164066f73d2379447033f5`.
`Score(question_type="systemone")` uses upstream decision tasks internally, not
HTTP. Plain Score keeps its existing admission checks. Older dependencies without
`server-decision.cpp` return gRPC `UNIMPLEMENTED` for this request type.

## Decision signature compatibility

This pin includes upstream Nimble support, in addition to OpenJev, Lev, Kev,
and Laya. The native bridge forwards the complete parsed question collection
when upstream's `fill_task` accepts it, as required by Nimble's schema framing.
`decision_compat.h` detects the callable C++ signature at compile time; older
native-decision forks still use their original single-question signature.
Forks without native decision support retain the existing `UNIMPLEMENTED` guard.
The standalone `decision_compat_test.cpp` checks both signatures and that the
full collection is passed by reference, not replaced with a singleton.
It is automatically discovered by `backend/cpp/run-unit-tests.sh`.

Signature and compile validation do not establish Nimble model accuracy or
runtime support for every artifact. Nimble weights are not part of this test
fixture. The official `ggml-org/Bespoke-Nimble-9B-v3-GGUF` model card declares
CC-BY-NC-4.0; check its restrictions before deployment.

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
For bounded image/projector support and its separate runtime checks, see
[README-decision-images.md](README-decision-images.md).

The metadata-stripped encoder with embeddings disabled and `-np 1` aborts
in warmup at `llama-context.cpp`'s output-budget assertion on **clean unpatched**
upstream at the pinned revision as well. This is a preexisting invalid-fixture
configuration hazard, not a decision-dispatch or Score/TTS patch regression.
Do not use that configuration as the missing-metadata test.
