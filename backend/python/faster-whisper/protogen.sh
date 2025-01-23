#!/bin/bash
set -e

source $(dirname $0)/../common/libbackend.sh

python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto