#!/bin/bash

## Patches

## Apply patches from the `patches` directory
if [ -d "patches" ]; then
    for patch in $(ls patches); do
        echo "Applying patch $patch"
        patch -d ik_llama.cpp/ -p1 < patches/$patch
    done
fi

set -e

for file in $(ls ik_llama.cpp/examples/server/); do
    cp -rfv ik_llama.cpp/examples/server/$file ik_llama.cpp/examples/grpc-server/
done

cp -r CMakeLists.txt ik_llama.cpp/examples/grpc-server/
cp -r grpc-server.cpp ik_llama.cpp/examples/grpc-server/
cp -rfv ik_llama.cpp/vendor/nlohmann/json.hpp ik_llama.cpp/examples/grpc-server/
cp -rfv ik_llama.cpp/vendor/cpp-httplib/httplib.h ik_llama.cpp/examples/grpc-server/

set +e
if grep -q "grpc-server" ik_llama.cpp/examples/CMakeLists.txt; then
    echo "grpc-server already added"
else
    echo "add_subdirectory(grpc-server)" >> ik_llama.cpp/examples/CMakeLists.txt
fi
set -e
