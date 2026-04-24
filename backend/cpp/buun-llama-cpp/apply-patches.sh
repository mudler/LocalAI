#!/bin/bash
# Apply the buun-llama-cpp patch series to a cloned buun-llama-cpp checkout.
#
# buun-llama-cpp is a fork-of-a-fork that branched off upstream llama.cpp
# before some API changes the shared backend/cpp/llama-cpp/grpc-server.cpp
# depends on. We carry those upstream commits as patch files under
# backend/cpp/buun-llama-cpp/patches/ and apply them here so the reused
# grpc-server source compiles against the fork unmodified.
#
# Drop the corresponding patch from patches/ whenever the fork catches up with
# upstream — the build will fail fast if a patch stops applying, which is the
# signal to retire it.

set -euo pipefail

if [[ $# -ne 2 ]]; then
    echo "usage: $0 <llama.cpp-src-dir> <patches-dir>" >&2
    exit 2
fi

SRC_DIR=$1
PATCHES_DIR=$2

if [[ ! -d "$SRC_DIR" ]]; then
    echo "source dir does not exist: $SRC_DIR" >&2
    exit 2
fi

if [[ ! -d "$PATCHES_DIR" ]]; then
    echo "no patches dir at $PATCHES_DIR, nothing to apply"
    exit 0
fi

shopt -s nullglob
patches=("$PATCHES_DIR"/*.patch)
shopt -u nullglob

if [[ ${#patches[@]} -eq 0 ]]; then
    echo "no .patch files in $PATCHES_DIR, nothing to apply"
    exit 0
fi

cd "$SRC_DIR"

for patch in "${patches[@]}"; do
    echo "==> applying $patch"
    git apply --verbose "$patch"
done

echo "all buun-llama-cpp patches applied successfully"
