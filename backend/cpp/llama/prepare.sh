#!/bin/bash

## Patches
## Apply patches from the `patches` directory
for patch in $(ls patches); do
    echo "Applying patch $patch"
    patch -d llama.cpp/ -p1 < patches/$patch
done 

cp -r CMakeLists.txt llama.cpp/examples/grpc-server/
cp -r grpc-server.cpp llama.cpp/examples/grpc-server/
cp -rfv json.hpp llama.cpp/examples/grpc-server/
cp -rfv utils.hpp llama.cpp/examples/grpc-server/
    
if grep -q "grpc-server" llama.cpp/examples/CMakeLists.txt; then
    echo "grpc-server already added"
else
    echo "add_subdirectory(grpc-server)" >> llama.cpp/examples/CMakeLists.txt
fi

## XXX: In some versions of CMake clip wasn't being built before llama.
## This is an hack for now, but it should be fixed in the future.
cp -rfv llama.cpp/examples/llava/clip.h llama.cpp/examples/grpc-server/clip.h
cp -rfv llama.cpp/examples/llava/llava.cpp llama.cpp/examples/grpc-server/llava.cpp
echo '#include "llama.h"' > llama.cpp/examples/grpc-server/llava.h
cat llama.cpp/examples/llava/llava.h >> llama.cpp/examples/grpc-server/llava.h
cp -rfv llama.cpp/examples/llava/clip.cpp llama.cpp/examples/grpc-server/clip.cpp