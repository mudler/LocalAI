#!/bin/bash
# Adapt the shared llama.cpp gRPC source to the JSON API in the pinned Bonsai tree.

set -euo pipefail

if [[ $# -ne 2 ]]; then
    echo "usage: $0 <grpc-server.cpp> <llama.cpp-dir>" >&2
    exit 2
fi

SRC=$1
LLAMA_CPP_DIR=$2
if [[ ! -f "$SRC" ]]; then
    echo "grpc-server.cpp not found at $SRC" >&2
    exit 2
fi

if [[ -f "$LLAMA_CPP_DIR/common/json.h" ]] && grep -q 'struct common_json_error' "$LLAMA_CPP_DIR/common/json.h"; then
    echo "==> $LLAMA_CPP_DIR uses common_json_error"
    awk '{ gsub(/json::parse_error/, "common_json_error"); print }' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"
elif grep -q 'common_json_error' "$SRC"; then
    echo "==> patching $SRC to use the Bonsai JSON exception type"
    awk '{ gsub(/common_json_error/, "json::parse_error"); print }' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"
    echo "==> Bonsai JSON exception patch OK"
else
    echo "==> $SRC already uses a Bonsai-compatible JSON exception type, skipping"
fi
