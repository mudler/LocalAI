#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")
cd "$CURDIR"

echo "Running vllm-cpp backend tests..."

# Unit specs always run (struct-mirror layout, option/sampling mapping, load
# validation). The e2e specs need a real model: set VLLM_CPP_MODEL to a .gguf
# file or a safetensors model dir to enable them (see e2e_test.go).
go test -v -timeout 1200s .

echo "All vllm-cpp tests passed."
