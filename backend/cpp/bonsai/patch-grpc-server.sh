#!/bin/bash
# Adapt the shared llama.cpp gRPC source to the older JSON API in Bonsai.

set -euo pipefail

if [[ $# -ne 1 ]]; then
    echo "usage: $0 <grpc-server.cpp>" >&2
    exit 2
fi

SRC=$1
if [[ ! -f "$SRC" ]]; then
    echo "grpc-server.cpp not found at $SRC" >&2
    exit 2
fi

if grep -q 'common_json_error' "$SRC"; then
    echo "==> patching $SRC to use the Bonsai JSON exception type"
    awk '{ gsub(/common_json_error/, "json::parse_error"); print }' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"
    echo "==> Bonsai JSON exception patch OK"
else
    echo "==> $SRC already uses a Bonsai-compatible JSON exception type, skipping"
fi
