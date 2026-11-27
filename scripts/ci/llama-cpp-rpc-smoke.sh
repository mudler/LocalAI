#!/usr/bin/env bash
# Load and run a model through llama.cpp's RPC backend, using the two RPC
# binaries of a llama-cpp backend image: llama-cpp-rpc-server as the worker and
# llama-cpp-grpc as the gRPC server that offloads to it.
#
# Both binaries are built in the GGML_BACKEND_DL tree and find the RPC backend
# (libggml-rpc.so) and the CPU variants in lib/ at run time, so this checks the
# packaging as well as the build: a missing library makes the worker fail to
# start or the gRPC server fail with "failed to find RPC backend".
#
# Usage: llama-cpp-rpc-smoke.sh <backend image>
set -euo pipefail

IMAGE=${1:?backend image is required}
PORT=${RPC_PORT:-50152}
REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=$(mktemp -d)
LOG="$WORK/rpc-server.log"
RPC_PID=""

cleanup() {
  if [ -n "$RPC_PID" ]; then kill "$RPC_PID" 2>/dev/null || true; fi
  echo "--- rpc-server log"; cat "$LOG" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

mkdir -p "$WORK/backend"
cid=$(docker create "$IMAGE" /run.sh)
docker export "$cid" | tar -xf - -C "$WORK/backend"
docker rm "$cid" > /dev/null

for bin in llama-cpp-grpc llama-cpp-rpc-server; do
  test -x "$WORK/backend/$bin" || { echo "image has no executable $bin" >&2; exit 1; }
done
test -e "$WORK/backend/lib/libggml-rpc.so" || { echo "image has no lib/libggml-rpc.so" >&2; exit 1; }

LLAMACPP_BINARY=llama-cpp-rpc-server bash "$WORK/backend/run.sh" -H 127.0.0.1 -p "$PORT" > "$LOG" 2>&1 &
RPC_PID=$!

for _ in $(seq 1 60); do
  if (echo > "/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then break; fi
  kill -0 "$RPC_PID" 2>/dev/null || { echo "rpc-server exited before it listened" >&2; exit 1; }
  sleep 1
done
(echo > "/dev/tcp/127.0.0.1/$PORT") 2>/dev/null || { echo "rpc-server did not listen on $PORT" >&2; exit 1; }

# run.sh picks llama-cpp-grpc when LLAMACPP_GRPC_SERVERS is set, and the
# backend process inherits it from the test suite.
LLAMACPP_GRPC_SERVERS="127.0.0.1:$PORT" \
BACKEND_IMAGE="$IMAGE" \
BACKEND_TEST_CAPS=health,load,predict \
  make -C "$REPO_ROOT" test-extra-backend

# The port probes above also open connections; the worker logs those as
# "Expected HELLO command". Only a real ggml RPC client gets past the hello.
accepted=$(grep -c 'Accepted client connection' "$LOG" || true)
probes=$(grep -c 'Expected HELLO command' "$LOG" || true)
[ "$accepted" -gt "$probes" ] || { echo "the gRPC server never connected to the RPC worker" >&2; exit 1; }
echo "PASS: model loaded and ran through the RPC worker"
