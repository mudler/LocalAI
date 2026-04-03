#!/bin/bash

SHARED_DIR="${SHARED_DIR:-.}"
SERVER_SOURCE_DIR="${SERVER_SOURCE_DIR:-tools/server}"
GRPC_SERVER_DIR="${GRPC_SERVER_DIR:-tools/grpc-server}"

## Apply patches from the `patches` directory
if [ -d "patches" ]; then
    for patch in $(ls patches); do
        echo "Applying patch $patch"
        patch -d llama.cpp/ -p1 < patches/$patch
    done
fi

set -e

# Copy server source files into grpc-server build directory
for file in $(ls llama.cpp/${SERVER_SOURCE_DIR}/); do
    cp -rfv llama.cpp/${SERVER_SOURCE_DIR}/$file llama.cpp/${GRPC_SERVER_DIR}/
done

# Copy build files — prefer local overrides, fall back to SHARED_DIR
for f in CMakeLists.txt grpc-server.cpp; do
    if [ -f "$f" ]; then
        cp -r "$f" llama.cpp/${GRPC_SERVER_DIR}/
    else
        cp -r "$SHARED_DIR/$f" llama.cpp/${GRPC_SERVER_DIR}/
    fi
done

cp -rfv llama.cpp/vendor/nlohmann/json.hpp llama.cpp/${GRPC_SERVER_DIR}/
cp -rfv llama.cpp/vendor/cpp-httplib/httplib.h llama.cpp/${GRPC_SERVER_DIR}/

# Add grpc-server subdirectory to the parent CMakeLists.txt
PARENT_CMAKELISTS="llama.cpp/$(dirname ${GRPC_SERVER_DIR})/CMakeLists.txt"

set +e
if grep -q "grpc-server" "$PARENT_CMAKELISTS"; then
    echo "grpc-server already added"
else
    echo "add_subdirectory(grpc-server)" >> "$PARENT_CMAKELISTS"
fi
set -e
