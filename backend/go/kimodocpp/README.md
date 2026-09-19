# kimodocpp

Persistent gRPC adapter for the pinned kimodo.cpp C API. The small C++ bridge
exposes upstream skeleton metadata and runtime configuration; Go exports the
returned motion as a node-only animated GLB. See
[3D animation](../../../docs/content/features/3d-animation.md) for the API and
runtime options.

```sh
make protogen-go
make -C backend/go/kimodocpp BUILD_TYPE=cpu
KIMODO_TEST_PACKAGE="$PWD/backend/go/kimodocpp/package" make -C backend/go/kimodocpp test
make -C backend/go/kimodocpp BUILD_TYPE=vulkan
```

Requires CMake 3.25+, a C++23 compiler, and Go. Vulkan additionally requires the
Vulkan SDK with shader tooling. Linux and macOS/Apple Silicon CPU are supported;
Metal is disabled because upstream implements CPU/Vulkan only.

Normal CI tests need no model downloads. To exercise real weights, set
`KIMODO_TEST_LIBRARY` to the built `libkimodo.so` (or dylib),
`KIMODO_TEST_MOTION` to a motion GGUF, `KIMODO_TEST_TEXT` to the complete text
GGUF beside `tokenizer.gguf` (or legacy bundle directory), and
`KIMODO_TEST_DEVICE=cpu` or `vulkan`, then run the tests.
The real-model test generates two clips with one session to cover reuse.
Set `KIMODO_TEST_TEXT_LAYER_CHUNK=8` to exercise bounded streaming instead of
the default all-layer residency.

Upstream now respects the configured thread count in both encoders and keeps
motion weights and execution graphs resident. The adapter defaults to 32 text
layers so the text weights also stay resident between requests. When bumping
the pinned upstream commit, verify the C ABI layout/version and all three
skeleton families. Pin changes update a clean cached checkout automatically;
local source modifications stop the update rather than being discarded. Preserve
any such changes before using `make clean` to replace that generated checkout.
