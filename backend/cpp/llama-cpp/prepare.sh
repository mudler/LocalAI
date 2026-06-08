#!/bin/bash

## Patches

## Apply patches from the `patches` directory.
##
## A carry-patch that no longer applies cleanly (context drift after a
## llama.cpp version bump) must FAIL THE BUILD here, with a clear message —
## not apply with fuzz, and not be silently skipped only to resurface as a
## confusing compile error hundreds of lines into the C++ build. So:
##   --fuzz=0  : any context mismatch is a hard failure (re-sync the patch);
##   exit 1    : abort prepare so CMake never runs against a half-patched tree.
## A reverse dry-run first lets a re-run on an already-patched tree be a no-op
## (idempotent) rather than a false failure.
if [ -d "patches" ]; then
    for patch in patches/*.patch; do
        [ -e "$patch" ] || continue   # glob matched nothing (no .patch files)
        name=$(basename "$patch")
        if patch -d llama.cpp/ -p1 -R --dry-run -f < "$patch" >/dev/null 2>&1; then
            echo "Patch $name already applied, skipping"
            continue
        fi
        echo "Applying patch $name"
        if ! patch -d llama.cpp/ -p1 --fuzz=0 --forward < "$patch"; then
            echo "ERROR: carry-patch $name did not apply cleanly to llama.cpp." >&2
            echo "       This usually means LLAMA_VERSION was bumped and the patch needs re-syncing." >&2
            exit 1
        fi
    done
fi

set -e

for file in $(ls llama.cpp/tools/server/); do
    cp -rfv llama.cpp/tools/server/$file llama.cpp/tools/grpc-server/
done

cp -r CMakeLists.txt llama.cpp/tools/grpc-server/
cp -r grpc-server.cpp llama.cpp/tools/grpc-server/
cp -rfv llama.cpp/vendor/nlohmann/json.hpp llama.cpp/tools/grpc-server/
cp -rfv llama.cpp/vendor/cpp-httplib/httplib.h llama.cpp/tools/grpc-server/

set +e
if grep -q "grpc-server" llama.cpp/tools/CMakeLists.txt; then
    echo "grpc-server already added"
else
    echo "add_subdirectory(grpc-server)" >> llama.cpp/tools/CMakeLists.txt
fi
set -e

