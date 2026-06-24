#!/bin/bash

## Patches

## Apply patches from the `patches` directory
if [ -d "patches" ]; then
    for patch in $(ls patches); do
        echo "Applying patch $patch"
        patch -d llama.cpp/ -p1 < patches/$patch
    done
fi

set -e

cp -r CMakeLists.txt llama.cpp/examples/grpc-server/
cp -r grpc-server.cpp llama.cpp/examples/grpc-server/
cp -r utils.hpp llama.cpp/examples/grpc-server/
cp -rfv llama.cpp/vendor/nlohmann/json.hpp llama.cpp/examples/grpc-server/

## Multimodal support is provided by the `mtmd` library target (examples/mtmd/),
## which the grpc-server links and includes directly. No source copy is needed:
## clip/llava were pruned upstream and the high-level mtmd_* API is used instead.

set +e
if grep -q "grpc-server" llama.cpp/examples/CMakeLists.txt; then
    echo "grpc-server already added"
else
    echo "add_subdirectory(grpc-server)" >> llama.cpp/examples/CMakeLists.txt
fi
set -e
