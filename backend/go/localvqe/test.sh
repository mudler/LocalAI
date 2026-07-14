#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")
cd "$CURDIR"

# The Go test suite uses a built localvqe binary for end-to-end
# specs. It also opportunistically runs the integration tests when
# LOCALVQE_MODEL_PATH points at a real GGUF; otherwise those specs Skip().

export LOCALVQE_BINARY="${LOCALVQE_BINARY:-$CURDIR/localvqe}"
export LD_LIBRARY_PATH="$CURDIR:$LD_LIBRARY_PATH"

go test -v ./...
