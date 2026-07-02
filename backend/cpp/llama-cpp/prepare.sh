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

for file in $(ls llama.cpp/tools/server/); do
    cp -rfv llama.cpp/tools/server/$file llama.cpp/tools/grpc-server/
done

cp -r CMakeLists.txt llama.cpp/tools/grpc-server/
cp -r grpc-server.cpp llama.cpp/tools/grpc-server/
# Shared message-reconstruction helpers (included by grpc-server.cpp) and their
# unit test (compiled only when -DLLAMA_GRPC_BUILD_TESTS=ON).
cp -r message_content.h llama.cpp/tools/grpc-server/
cp -r message_content_test.cpp llama.cpp/tools/grpc-server/
# Parent-death watcher (included by grpc-server.cpp) and its standalone unit
# test (run via backend/cpp/run-unit-tests.sh; also buildable under ctest).
cp -r parent_watch.h llama.cpp/tools/grpc-server/
cp -r parent_watch_test.cpp llama.cpp/tools/grpc-server/
cp -rfv llama.cpp/vendor/nlohmann/json.hpp llama.cpp/tools/grpc-server/
cp -rfv llama.cpp/vendor/cpp-httplib/httplib.h llama.cpp/tools/grpc-server/

set +e
if grep -q "grpc-server" llama.cpp/tools/CMakeLists.txt; then
    echo "grpc-server already added"
else
    echo "add_subdirectory(grpc-server)" >> llama.cpp/tools/CMakeLists.txt
fi
set -e

