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

## Copy clip/llava files for multimodal support (built as myclip library)
cp -rfv llama.cpp/examples/llava/clip.h llama.cpp/examples/grpc-server/clip.h
cp -rfv llama.cpp/examples/llava/clip.cpp llama.cpp/examples/grpc-server/clip.cpp
cp -rfv llama.cpp/examples/llava/llava.cpp llama.cpp/examples/grpc-server/llava.cpp
# Prepend llama.h include to llava.h
echo '#include "llama.h"' > llama.cpp/examples/grpc-server/llava.h
cat llama.cpp/examples/llava/llava.h >> llama.cpp/examples/grpc-server/llava.h
# Copy clip-impl.h if it exists
if [ -f llama.cpp/examples/llava/clip-impl.h ]; then
    cp -rfv llama.cpp/examples/llava/clip-impl.h llama.cpp/examples/grpc-server/clip-impl.h
fi
# Copy stb_image.h
if [ -f llama.cpp/vendor/stb/stb_image.h ]; then
    cp -rfv llama.cpp/vendor/stb/stb_image.h llama.cpp/examples/grpc-server/stb_image.h
elif [ -f llama.cpp/common/stb_image.h ]; then
    cp -rfv llama.cpp/common/stb_image.h llama.cpp/examples/grpc-server/stb_image.h
fi

## Fix API compatibility in llava.cpp (llama_n_embd -> llama_model_n_embd)
if [ -f llama.cpp/examples/grpc-server/llava.cpp ]; then
    sed -i 's/llama_n_embd(/llama_model_n_embd(/g' llama.cpp/examples/grpc-server/llava.cpp
fi

set +e
if grep -q "grpc-server" llama.cpp/examples/CMakeLists.txt; then
    echo "grpc-server already added"
else
    echo "add_subdirectory(grpc-server)" >> llama.cpp/examples/CMakeLists.txt
fi
set -e
