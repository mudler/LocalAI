#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# backend.proto lives at repo backend/; from backend/python/spiritlm that is ../../../backend
proto_root="${backend_dir}/../../../backend"
python3 -m grpc_tools.protoc -I"${proto_root}" --python_out=. --grpc_python_out=. "${proto_root}/backend.proto"
