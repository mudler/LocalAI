#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# This is here because the Intel pip index is broken and returns 200 status codes for every package name, it just doesn't return any package links.
# This makes uv think that the package exists in the Intel pip index, and by default it stops looking at other pip indexes once it finds a match.
# We need uv to continue falling through to the pypi default index to find optimum[openvino] in the pypi index
# the --upgrade actually allows us to *downgrade* torch to the version provided in the Intel pip index
if [ "x${BUILD_PROFILE}" == "xintel" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
fi

if [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
  PYTHON_VERSION="3.12"
  PYTHON_PATCH="12"
  PY_STANDALONE_TAG="20251120"
fi

if [ "x${BUILD_PROFILE}" == "xl4t12" ]; then
    USE_PIP=true
fi

if [ "x${BUILD_PROFILE}" == "xhipblas" ]; then
  ensureVenv
  mkdir /tmp/ctranslate2-rocm
  wget -O /tmp/ctranslate2-rocm/rocm-python-wheels-Linux.zip https://github.com/OpenNMT/CTranslate2/releases/download/v4.7.1/rocm-python-wheels-Linux.zip
  unzip /tmp/ctranslate2-rocm/rocm-python-wheels-Linux.zip -d /tmp/ctranslate2-rocm/
  python3 -m ensurepip
  python3 -m pip install --no-dependencies --no-index --find-links=/tmp/ctranslate2-rocm/temp-linux/ ctranslate2
fi

installRequirements
