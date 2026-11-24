#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# Start backend in background (use env to avoid port conflict in parallel tests)
export PYTHONUNBUFFERED=1
BACKEND_PORT=${BACKEND_PORT:-50051}
python backend.py --addr "localhost:${BACKEND_PORT}" &
BACKEND_PID=$!
trap "kill $BACKEND_PID 2>/dev/null || true" EXIT
sleep 3
export BACKEND_PORT
runUnittests
