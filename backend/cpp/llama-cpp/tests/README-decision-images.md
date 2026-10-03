# Native decision images

Run from the repository root after obtaining the pinned llama.cpp checkout:

```sh
bash backend/cpp/llama-cpp/tests/verify-decision-images.sh
```

This compiles the production validation helper with upstream stb. It checks
strict base64, MIME matching, count/byte/dimension/pixel limits, PNG decompression
bombs, truncation, chat-only collection, Anthropic normalization, empty-image
text limits, capability combinations, and parity with Go's canonical limits.
Fixtures are generated locally; no image or model downloads occur.

Build the native backend normally with `make backends/llama-cpp`. For a prepared
CPU checkout and extracted distro gRPC dependencies, the existing adapter is:

```sh
DEPS_ROOT=/path/to/deps CPU_BUILD=/path/to/llama.cpp/build-cpu \
  bash backend/cpp/llama-cpp/tests/build-decision-bridge.sh
```

The adapter compiles the actual prepared grpc-server.cpp and links upstream CPU
libraries. It is not a replacement implementation or mocked backend.

Start the resulting `grpc-server --addr=127.0.0.1:50061`, then run:

```sh
PYTHONPATH=/path/to/build-decision-validation \
python3 backend/cpp/llama-cpp/tests/decision-image-smoke.py \
  --model /models/OpenJev-Q4_K_M.gguf \
  --projector /models/mmproj-OpenJev-Q8_0.gguf \
  --fixtures backend/cpp/llama-cpp/llama.cpp/build-image-tests/fixtures.json
```

The smoke uses CPU only, four threads, one slot, 8192 context, batch 512. It first
loads without a projector and asserts explicit unsupported plus direct-RPC
safety errors, then loads with the projector and compares red/blue probabilities.
It requires existing weights and a checksum-verified projector; it downloads
nothing. These are direct RPC tests, **not** public HTTP/router E2E evidence.
