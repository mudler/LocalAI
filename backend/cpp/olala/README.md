# olala backend

A llama.cpp variant built from the [OVHai-LLM/llama.cpp](https://github.com/OVHai-LLM/llama.cpp)
fork, branch `olala-master`. The fork adds the `olala` architecture (a Mamba3 hybrid
mixture-of-experts) that upstream llama.cpp cannot load. It serves the GGUF files of
[OVHaiLLM/Olala-v1.0-mini-7A1B-GGUF](https://huggingface.co/OVHaiLLM/Olala-v1.0-mini-7A1B-GGUF).
It reuses the gRPC server sources of `backend/cpp/llama-cpp`.

## The pin is manual

`OLALA_VERSION` in `Makefile` is a commit of the fork. This backend is deliberately **not**
in `.github/workflows/bump_deps.yaml`: the fork branched from upstream some commits earlier,
so a new fork commit can break the shared gRPC server, and a human must check each bump.

To bump the pin:

1. Pick a commit on `olala-master` of the fork.
2. Set `OLALA_VERSION` in `backend/cpp/olala/Makefile` to its full SHA.
3. Build: `make backends/olala` (or `make -C backend/cpp/olala olala-fallback`).
4. If `apply-patches.sh` rejects a patch in `patches/`, the fork has caught up or moved:
   re-cut or drop that patch (see `patches/README.md`).
5. Load an Olala GGUF and run a chat completion before you open the PR.

## Build flags

`OLALA_CMAKE_ARGS` in `Makefile` sets `-DGGML_CPU_REPACK=OFF` for every flavor.
