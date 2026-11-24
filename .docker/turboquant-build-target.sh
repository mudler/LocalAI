#!/usr/bin/env bash
set -euo pipefail

# Every image flavor ships turboquant-cpu-all: one loader plus, for a GPU image, its own
# backend, with ggml choosing the CPU variant at run time. The variants are built by their
# own stage (.docker/llama-cpp-cpu-variants.sh) from the fork's pin with plain gcc, so the
# GPU toolchains never compile them. See .docker/llama-cpp-build-target.sh for what that
# replaced. The arguments stay so callers and the test keep one interface.
: "${1:?target architecture is required}"
echo turboquant-cpu-all
