#!/bin/bash
# Assemble backend/cpp/gufo/package, which becomes the whole content of the
# FROM scratch backend image. Nothing outside this directory exists at run time.
# gufo builds only for Linux x86-64 with ROCm, so there is no Darwin branch.
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."
PACKAGE_DIR="$CURDIR/package"

rm -rf "$PACKAGE_DIR"
mkdir -p "$PACKAGE_DIR/lib"

cp -avf "$CURDIR/grpc-server" "$PACKAGE_DIR/"
cp -fv  "$CURDIR/run.sh"      "$PACKAGE_DIR/"

# gufo's license, NOTICE and the third-party notices of the code it vendors
# must ship with the binary.
GUFO_SRC="$CURDIR/gufo"
if [ ! -f "$GUFO_SRC/LICENSE" ]; then
    echo "package.sh: $GUFO_SRC/LICENSE is missing; the gufo checkout is incomplete" >&2
    exit 1
fi
mkdir -p "$PACKAGE_DIR/licenses/gufo"
cp -fv "$GUFO_SRC/LICENSE" "$PACKAGE_DIR/licenses/gufo/"
for notice in NOTICE THIRD_PARTY_NOTICES.md; do
    if [ -f "$GUFO_SRC/$notice" ]; then
        cp -fv "$GUFO_SRC/$notice" "$PACKAGE_DIR/licenses/gufo/"
    fi
done
if [ -d "$GUFO_SRC/licenses" ]; then
    cp -rfv "$GUFO_SRC/licenses" "$PACKAGE_DIR/licenses/gufo/"
fi

# Loader and C/C++ runtime into lib/, where run.sh execs lib/ld.so from.
# shellcheck source=/dev/null
source "$REPO_ROOT/scripts/build/package-system-libs.sh" "$PACKAGE_DIR/lib" ""

# Bundle the full dependency closure. grpc-server links the distro gRPC,
# protobuf and absl stack plus ICU, curl, libpng and libjpeg for gufo; copying
# only the C/C++ runtime leaves the scratch image unable to start.
ldd "$CURDIR/grpc-server" | awk '$2 == "=>" && $3 ~ /^\// { print $3 }' | sort -u | \
while read -r so; do
    cp -arfLv "$so" "$PACKAGE_DIR/lib/"
done

GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    # BUILD_TYPE=hipblas dispatches to package_rocm_libs, which also copies the
    # rocBLAS and hipBLASLt kernel data that run.sh points the libraries at.
    # shellcheck source=/dev/null
    source "$GPU_LIB_SCRIPT" "$PACKAGE_DIR/lib"
    package_gpu_libs
fi

# Resolve every dependency through the same loader and library path the
# from-scratch image uses. The loader can still fall back to the host's default
# directories, so reject both a dependency it could not resolve and one it
# resolved OUTSIDE the package, which would validate here and be absent in the
# image. LD_TRACE_LOADED_OBJECTS is used instead of `ld.so --list` because
# --list aborts on the first missing library instead of reporting it.
validation_failed=0
validate_object() {
    local object="$1"
    LD_TRACE_LOADED_OBJECTS=1 LD_LIBRARY_PATH="$PACKAGE_DIR/lib" \
        "$PACKAGE_DIR/lib/ld.so" "$object" | awk -v pkg="$PACKAGE_DIR/" -v obj="$object" '
        $2 == "=>" && $3 == "not" {
            print "package.sh: unresolved dependency of " obj ": " $1 > "/dev/stderr"
            bad = 1
        }
        $2 == "=>" && $3 ~ /^\// && index($3, pkg) != 1 {
            print "package.sh: dependency of " obj " resolved outside the package: " $0 > "/dev/stderr"
            bad = 1
        }
        END { exit bad }
    '
}

validate_object "$PACKAGE_DIR/grpc-server" || validation_failed=1
if [ "$validation_failed" -ne 0 ]; then
    exit 1
fi

echo "gufo package contents:"
ls -lah "$PACKAGE_DIR/" "$PACKAGE_DIR/lib/"
