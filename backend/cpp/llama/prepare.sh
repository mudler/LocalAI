#!/bin/bash

## Patches
## Apply patches from the `patches` directory
for patch in $(ls patches); do
    echo "Applying patch $patch"
    patch -d llama.cpp/ -p1 < patches/$patch
done 

cp -r CMakeLists.txt llama.cpp/tools/grpc-server/
cp -r grpc-server.cpp llama.cpp/tools/grpc-server/
cp -rfv json.hpp llama.cpp/tools/grpc-server/
cp -rfv utils.hpp llama.cpp/tools/grpc-server/
    
if grep -q "grpc-server" llama.cpp/tools/CMakeLists.txt; then
    echo "grpc-server already added"
else
    echo "add_subdirectory(grpc-server)" >> llama.cpp/tools/CMakeLists.txt
fi

## XXX: In some versions of CMake clip wasn't being built before llama.
## This is an hack for now, but it should be fixed in the future.
cp -rfv llama.cpp/tools/mtmd/clip.h llama.cpp/tools/grpc-server/clip.h
cp -rfv llama.cpp/tools/mtmd/clip-impl.h llama.cpp/tools/grpc-server/clip-impl.h
cp -rfv llama.cpp/tools/mtmd/llava.cpp llama.cpp/tools/grpc-server/llava.cpp
echo '#include "llama.h"' > llama.cpp/tools/grpc-server/llava.h
cat llama.cpp/tools/mtmd/llava.h >> llama.cpp/tools/grpc-server/llava.h
cp -rfv llama.cpp/tools/mtmd/clip.cpp llama.cpp/tools/grpc-server/clip.cpp