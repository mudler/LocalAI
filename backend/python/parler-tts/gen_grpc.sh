#!/bin/bash
set -ex
## A bash script wrapper that runs the transformers server with conda
## It uses the protoc compiler to generate the gRPC code from the environment, because
## The newer grpc versions are not compatible
#See: https://github.com/mudler/LocalAI/pull/2027

# Activate conda environment
source activate parler

python3 -m grpc_tools.protoc -I../.. --python_out=. --grpc_python_out=. backend.proto