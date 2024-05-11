#!/bin/bash
set -e

LIMIT_TARGETS="cublas"
EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"
EXLLAMA2_VERSION=c0ddebaaaf8ffd1b3529c2bb654e650bce2f790f

source $(dirname $0)/../common/libbackend.sh

installRequirements

git clone https://github.com/turboderp/exllamav2 $MY_DIR/source
pushd ${MY_DIR}/source && git checkout -b build ${EXLLAMA2_VERSION} && popd

# This installs exllamav2 in JIT mode so it will compile the appropriate torch extension at runtime
EXLLAMA_NOCOMPILE= uv pip install ${EXTRA_PIP_INSTALL_FLAGS} ${MY_DIR}/source/
