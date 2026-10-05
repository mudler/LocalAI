#!/bin/bash
set -e
python3 -m grpc_tools.protoc -I../../ --python_out=. --grpc_python_out=. ../../backend.proto
