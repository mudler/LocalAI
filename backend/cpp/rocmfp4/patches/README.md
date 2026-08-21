# rocmfp4 fork skew patches

The `rocmfp4` backend reuses `backend/cpp/llama-cpp/grpc-server.cpp` (written against
LocalAI's pinned *upstream* llama.cpp) but compiles it against the
[walcz-de/llama.cpp-ROCmFP4](https://github.com/walcz-de/llama.cpp-ROCmFP4) fork, which
carries the ROCmFP4 / ROCmFPx tensor formats (ggml types 100-107) for AMD RDNA3.5 APUs.

The fork tracks the same upstream pin as `backend/cpp/llama-cpp` (it is re-based onto the
`LLAMA_VERSION` LocalAI pins), so in the common case **no patches are needed here** and this
directory stays empty. If the pins ever drift — upstream changes an API the shared gRPC
server depends on before the fork has rebased — the gap is back-ported here as a `*.patch`
file and applied to the cloned fork checkout by `../apply-patches.sh`.

Patches apply with `git apply` from the fork checkout root. Name them
`NNNN-short-description.patch` so the apply order stays deterministic.
