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
bundle directory, and `KIMODO_TEST_DEVICE=cpu` or `vulkan`, then run the tests.
The real-model test generates two clips with one session to cover reuse.

The text-thread patch makes the upstream text encoder respect the configured
thread count, matching the denoiser. When bumping the pinned upstream commit,
verify the patch, C ABI layout/version, and all three skeleton families.
