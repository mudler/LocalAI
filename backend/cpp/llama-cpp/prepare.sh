#!/bin/bash

## Patches

## Apply patches: the base `patches/` series, then the gated `patches/paged/`
## series (default on; LLAMA_PAGED=off skips it). Runs before `set -e` so a
## re-apply on rebuild is tolerated. Only *.patch files are applied (docs/dirs
## like patches/paged/ and *.md are skipped).
if [ -d "patches" ]; then
    for patch in patches/*.patch; do
        [ -e "$patch" ] || continue
        echo "Applying patch $patch"
        patch -d llama.cpp/ -p1 < "$patch"
    done
    if [ "${LLAMA_PAGED:-on}" != "off" ] && [ -d "patches/paged" ]; then
        for patch in patches/paged/*.patch; do
            [ -e "$patch" ] || continue
            echo "Applying paged patch $patch"
            patch -d llama.cpp/ -p1 < "$patch"
        done
    fi
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

