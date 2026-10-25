#!/bin/bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
MAKEFILE="$ROOT/backend/cpp/bonsai/Makefile"
GRPC_SERVER="$ROOT/backend/cpp/llama-cpp/grpc-server.cpp"

grep -q 'catch (const common_json_error& e)' "$GRPC_SERVER"

if grep -q 'patch-grpc-server.sh' "$MAKEFILE"; then
    echo "Bonsai must preserve the common_json_error API provided by its pinned fork" >&2
    exit 1
fi

grep -q -- '--target grpc-server --target ggml-rpc-server' "$MAKEFILE"
grep -q '/llama.cpp/build/bin/ggml-rpc-server bonsai-rpc-server' "$MAKEFILE"

echo "PASS: Bonsai uses its pinned fork's JSON and RPC APIs"
