#!/bin/bash
set -e

source $(dirname $0)/../common/libbackend.sh

installRequirements

# https://github.com/descriptinc/audiotools/issues/101
# incompatible protobuf versions.
PYDIR=$(ls ${MY_DIR}/venv/lib)
curl -L https://raw.githubusercontent.com/protocolbuffers/protobuf/main/python/google/protobuf/internal/builder.py -o ${MY_DIR}/venv/lib/${PYDIR}/site-packages/google/protobuf/internal/builder.py
