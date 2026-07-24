#!/bin/bash
set -euo pipefail
CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."
PACKAGE_DIR="$CURDIR/package"

rm -rf "$PACKAGE_DIR"
mkdir -p "$PACKAGE_DIR/lib"
cp -avf "$CURDIR/grpc-server" "$PACKAGE_DIR/"
cp -avf "$CURDIR/ds4-worker"  "$PACKAGE_DIR/"
cp -rfv "$CURDIR/run.sh"      "$PACKAGE_DIR/"

UNAME_S=$(uname -s)
if [ "$UNAME_S" = "Darwin" ]; then
    # Darwin: bundle dylibs via otool -L (handled by scripts/build/ds4-darwin.sh).
    echo "package.sh: Darwin handled by ds4-darwin.sh"
    exit 0
fi

source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

# Bundle the complete dependency closure for both executables. In particular,
# grpc-server links the distro gRPC/protobuf/absl stack; copying only the core
# C/C++ runtime libraries leaves the scratch image unable to start.
{
    ldd "$CURDIR/grpc-server"
    ldd "$CURDIR/ds4-worker"
} | awk '$2 == "=>" && $3 ~ /^\// { print $3 }' | sort -u | \
while read -r so; do
    cp -arfLv "$so" "$PACKAGE_DIR/lib/"
done

GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    # shellcheck source=/dev/null
    source "$GPU_LIB_SCRIPT" "$PACKAGE_DIR/lib"
    package_gpu_libs
fi

# Resolve every dependency through the same loader and library path used by
# the from-scratch image. The loader can still search host defaults, so reject
# any absolute dependency path that escapes the package instead of accepting a
# false-positive validation against a library that scratch will not contain.
validate_packaged_binary() {
    local binary="$1"
    local resolution
    resolution=$("$PACKAGE_DIR/lib/ld.so" \
        --library-path "$PACKAGE_DIR/lib" \
        --list "$PACKAGE_DIR/$binary")

    printf '%s\n' "$resolution" | awk -v prefix="$PACKAGE_DIR/lib/" '
        $2 == "=>" && $3 ~ /^\// && index($3, prefix) != 1 {
            print "package.sh: dependency resolved outside package: " $0 > "/dev/stderr"
            invalid = 1
        }
        END { exit invalid }
    '
}

for binary in grpc-server ds4-worker; do
    validate_packaged_binary "$binary"
done

echo "ds4 package contents:"
ls -lah "$PACKAGE_DIR/" "$PACKAGE_DIR/lib/"
