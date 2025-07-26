#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

ensureVenv

python3 -m grpc_tools.protoc -I../.. -I./ --python_out=. --grpc_python_out=. backend.proto