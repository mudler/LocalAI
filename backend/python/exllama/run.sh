#!/bin/bash
LIMIT_TARGETS="cublas"
BACKEND_FILE="${MY_DIR}/source/backend.py"

source $(dirname $0)/../common/libbackend.sh

startBackend $@