#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")
REPO_ROOT="${CURDIR}/../../.."

mkdir -p $CURDIR/package/lib

cp -avf $CURDIR/sherpa-onnx $CURDIR/package/
cp -avf $CURDIR/run.sh $CURDIR/package/
cp -rfLv $CURDIR/backend-assets/lib/* $CURDIR/package/lib/

source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "Packaging completed successfully"
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/
