#!/bin/bash
# Assemble backend/cpp/audio-cpp/package, which becomes the whole content of the
# FROM scratch backend image. Nothing outside this directory exists at run time.
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."
PACKAGE_DIR="$CURDIR/package"
BUILD_DIR="$CURDIR/build"

rm -rf "$PACKAGE_DIR"
mkdir -p "$PACKAGE_DIR/lib" "$PACKAGE_DIR/assets"

cp -avf "$CURDIR/grpc-server" "$PACKAGE_DIR/"
cp -fv  "$CURDIR/run.sh"      "$PACKAGE_DIR/"

# ENGINE_ENABLE_CPU_ALL_VARIANTS builds the ggml backends as shared objects that
# are dlopened at run time, so ldd cannot see them and the dependency walk below
# would leave the image with no CPU backend at all. They also cannot go in lib/:
# ggml DISCOVERS them by listing dirname(/proc/self/exe) and the current
# directory, so being on a library path is not enough, they have to be in a
# directory ggml scans. run.sh execs the bundled loader from the package root
# exactly so that directory is this one. cmake writes them to build/bin, not
# next to build/grpc-server, which is why this reads from bin/.
#
# -a keeps the libggml.so -> libggml.so.0 -> libggml.so.0.12.0 symlink chain,
# so the SONAME the binary asks for still names a file here.
for pattern in '*.so*' '*.dylib*'; do
    if compgen -G "$BUILD_DIR/bin/$pattern" > /dev/null; then
        # shellcheck disable=SC2086
        cp -avf "$BUILD_DIR/bin/"$pattern "$PACKAGE_DIR/"
    fi
done

# Upstream ships silero_vad and marblenet_vad as small runtime assets.
# resolve_model_path() expands "bundled:<name>" to
# dirname(/proc/self/exe)/assets/<name>, so copying them here is what makes VAD
# work with nothing downloaded.
for asset in silero_vad marblenet_vad; do
    src="$CURDIR/audio.cpp/assets/framework/models/$asset"
    if [ -d "$src" ]; then
        cp -rfv "$src" "$PACKAGE_DIR/assets/"
    else
        echo "package.sh: bundled asset missing: $src" >&2
        echo "package.sh: run 'make audio.cpp' before packaging" >&2
        exit 1
    fi
done

UNAME_S=$(uname -s)
if [ "$UNAME_S" = "Darwin" ]; then
    echo "package.sh: Darwin dylib bundling is handled by the darwin build script"
    ls -lah "$PACKAGE_DIR/" "$PACKAGE_DIR/assets/"
    exit 0
fi

# The loader goes in the package ROOT, not in lib/. run.sh explains why at
# length; the short version is that exec'ing it makes dirname(/proc/self/exe)
# the directory it sits in, and both the ggml backend scan and the bundled:
# asset lookup need that to be the package root.
if [ -f "/lib64/ld-linux-x86-64.so.2" ]; then
    cp -arfLv /lib64/ld-linux-x86-64.so.2 "$PACKAGE_DIR/ld.so"
elif [ -f "/lib/ld-linux-aarch64.so.1" ]; then
    cp -arfLv /lib/ld-linux-aarch64.so.1 "$PACKAGE_DIR/ld.so"
else
    echo "package.sh: unknown architecture" >&2
    exit 1
fi

# Libraries the host GPU driver stack owns. package_gpu_libs deliberately ships
# the CUDA/Vulkan runtime but not the driver, because the driver has to match
# the kernel module on whatever host runs the image. Copying the build host's
# copy in would pin it to the build host instead.
is_driver_lib() {
    case "$(basename "$1")" in
        libcuda.so*|libnvidia-*) return 0 ;;
        *) return 1 ;;
    esac
}

# Bundle the full dependency closure. grpc-server links the distro gRPC,
# protobuf and absl stack; copying only the C/C++ runtime leaves the scratch
# image unable to start. The walk runs over the PACKAGED binary, not the one in
# $CURDIR, because its RUNPATH is $ORIGIN: only from inside the package does
# libggml.so.0 resolve to the copy shipped above rather than to nothing.
# The dlopened ggml objects are walked too, since a dependency of theirs that
# grpc-server does not itself link would otherwise be missed.
{
    ldd "$PACKAGE_DIR/grpc-server"
    for so in "$PACKAGE_DIR"/*.so*; do
        [ -f "$so" ] || continue
        ldd "$so"
    done
} | awk '$2 == "=>" && $3 ~ /^\// { print $3 }' | sort -u | \
while read -r so; do
    # Skip what is already inside the package: the ggml objects resolve through
    # $ORIGIN and re-copying them into lib/ would ship two copies of each.
    case "$so" in "$PACKAGE_DIR"/*) continue ;; esac
    if is_driver_lib "$so"; then
        echo "package.sh: leaving driver-owned library to the host: $so"
        continue
    fi
    cp -arfLv "$so" "$PACKAGE_DIR/lib/"
done

GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    # shellcheck source=/dev/null
    source "$GPU_LIB_SCRIPT" "$PACKAGE_DIR/lib"
    package_gpu_libs
fi

# Resolve every dependency through the same loader and library path the
# from-scratch image uses. Two distinct failures are rejected, because the
# loader can still fall back to the host's default directories: a dependency it
# could not resolve at all, and one it resolved to a file OUTSIDE the package,
# which would validate here and be absent in the image.
validation_failed=0
validate_object() {
    local object="$1"
    "$PACKAGE_DIR/ld.so" --library-path "$PACKAGE_DIR/lib:$PACKAGE_DIR" \
        --list "$object" | awk -v pkg="$PACKAGE_DIR/" -v obj="$object" '
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
for so in "$PACKAGE_DIR"/*.so*; do
    [ -f "$so" ] || continue
    validate_object "$so" || validation_failed=1
done
if [ "$validation_failed" -ne 0 ]; then
    exit 1
fi

echo "audio-cpp package contents:"
ls -lah "$PACKAGE_DIR/" "$PACKAGE_DIR/lib/" "$PACKAGE_DIR/assets/"
