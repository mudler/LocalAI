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

# Everything below this point is Linux-only: a bundled ELF loader, an ldd walk
# and an ld.so --list validation. The macOS equivalent is the otool -L closure in
# scripts/build/audio-cpp-darwin.sh, which picks up from the exit 0 below.
#
# WARNING FOR ANYONE REWORKING THAT SCRIPT. The obvious move is to copy
# scripts/build/privacy-filter-darwin.sh, and that script assembles its own
# package under build/darwin and never calls package.sh at all. Adapted as-is it
# will silently omit assets/, and the bundled: model path form then resolves to
# nothing, which takes the only zero-download verification path in this backend
# with it. That is why audio-cpp-darwin.sh copies THIS directory instead of
# rebuilding one. The Darwin package needs the same root-level layout as the
# Linux one: grpc-server, run.sh, the ggml dylibs and assets/ in ONE directory,
# with lib/ for the rest. run.sh's Darwin branch execs grpc-server directly, so
# _NSGetExecutablePath already names the package root; nothing else is needed
# beyond putting the files there.
UNAME_S=$(uname -s)
if [ "$UNAME_S" = "Darwin" ]; then
    echo "package.sh: Darwin dylib bundling is deferred to scripts/build/audio-cpp-darwin.sh"
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

# THE LAYOUT ASSERTION. Everything else in this script checks that the package
# can LINK. This checks that it can RESOLVE, which is a different property and
# the one with no other guard on it.
#
# The loader, assets/ and the dlopened ggml objects only agree while they share
# one directory, because run.sh execs the loader and all three are reached
# through dirname(/proc/self/exe). A tidy-up that moves the loader into lib/,
# following the llama-cpp layout, produces a package that builds, ships, and
# then fails at run time with "model path does not exist: <pkg>/lib/assets/..."
# or "Failed to initialize CPU backend". Fail the build instead.
#
# This sits immediately after the loader copy rather than at the end of the
# script on purpose: everything below dereferences $PACKAGE_DIR/ld.so, so a
# misplaced loader would otherwise surface as "No such file or directory" from
# the validation gate and never reach an assertion that could explain it.
if [ ! -f "$PACKAGE_DIR/ld.so" ]; then
    echo "package.sh: the bundled loader must be at the package root, not in lib/." >&2
    echo "package.sh: run.sh execs it, so its directory is dirname(/proc/self/exe)," >&2
    echo "package.sh: which is where resolve_model_path looks for assets/ and where" >&2
    echo "package.sh: ggml looks for the CPU variants." >&2
    exit 1
fi
if [ ! -d "$PACKAGE_DIR/assets" ]; then
    echo "package.sh: assets/ must sit beside the loader at the package root." >&2
    exit 1
fi
# Only assert the ggml half when this build produced CPU variants at all: a
# cublas or vulkan build links ggml statically and ships none.
if compgen -G "$BUILD_DIR/bin/libggml-cpu-*.so" > /dev/null && \
   ! compgen -G "$PACKAGE_DIR/libggml-cpu-*.so" > /dev/null; then
    echo "package.sh: the build produced libggml-cpu-*.so but none reached the" >&2
    echo "package.sh: package root, so ggml's scan of dirname(/proc/self/exe)" >&2
    echo "package.sh: will find no CPU backend." >&2
    exit 1
fi

# Libraries the host GPU driver stack owns. package_gpu_libs deliberately ships
# the CUDA/Vulkan runtime but not the driver, because the driver has to match
# the kernel module on whatever host runs the image. Copying the build host's
# copy in would pin it to the build host instead.
#
# One regex, used by both the copy loop and the validation gate below. They have
# to agree: exempting a library from the copy but not from the gate makes the
# gate reject the very absence the copy loop just created.
DRIVER_LIB_RE='^(libcuda\.so|libnvidia-)'
# awk applies string-escape processing to a -v assignment before compiling the
# regex, so a lone backslash is eaten and awk warns about it. Double them here
# rather than keeping a second hand-written copy of the pattern, which is the
# drift this single-source-of-truth exists to prevent.
DRIVER_LIB_RE_AWK=${DRIVER_LIB_RE//\\/\\\\}
is_driver_lib() {
    [[ "$(basename "$1")" =~ $DRIVER_LIB_RE ]]
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
#
# The driver libraries are exempt from BOTH rejections, and that exemption is
# load-bearing on GPU builds rather than tidiness. With BUILD_TYPE=cublas ggml
# is static (no CPU_ALL_VARIANTS), and ggml/CMakeLists.txt defaults
# GGML_CUDA_NO_VMM=OFF, so ggml-cuda links CUDA::cuda_driver and grpc-server
# itself carries DT_NEEDED libcuda.so.1. The copy loop above deliberately leaves
# that to the host, so inside the CUDA builder it resolves either to a host path
# or to nothing. Without this exemption every cublas build would fail here and
# CI would produce no image at all.
#
# LD_TRACE_LOADED_OBJECTS + LD_LIBRARY_PATH, NOT `ld.so --library-path --list`,
# and the difference is not cosmetic. Measured on a stub object built to
# DT_NEEDED an absent libcuda.so.1: `--list` refuses to trace at all, printing
# "libdrivertest.so: error while loading shared libraries: libcuda.so.1: cannot
# open shared object file" and exiting 127, so no per-library line is ever
# produced and no exemption below could apply. The env form prints
# "libcuda.so.1 => not found" and exits 0, which is what makes both the
# unresolved rule and its driver exemption reachable. It is also closer to what
# run.sh actually does, since run.sh exports LD_LIBRARY_PATH rather than passing
# --library-path.
validation_failed=0
validate_object() {
    local object="$1"
    LD_TRACE_LOADED_OBJECTS=1 LD_LIBRARY_PATH="$PACKAGE_DIR/lib:$PACKAGE_DIR" \
        "$PACKAGE_DIR/ld.so" "$object" | awk -v pkg="$PACKAGE_DIR/" -v obj="$object" \
                                             -v driver_re="$DRIVER_LIB_RE_AWK" '
        function base(p,   n, parts) { n = split(p, parts, "/"); return parts[n] }
        $2 == "=>" && $3 == "not" {
            if ($1 ~ driver_re) next
            print "package.sh: unresolved dependency of " obj ": " $1 > "/dev/stderr"
            bad = 1
        }
        $2 == "=>" && $3 ~ /^\// && index($3, pkg) != 1 {
            if (base($3) ~ driver_re) next
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
