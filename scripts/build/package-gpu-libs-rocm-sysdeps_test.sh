#!/bin/bash
# Regression test for the ROCm 10.x transitive-dependency sweep in
# scripts/build/package-gpu-libs.sh.
#
# ROCm 10.x (TheRock packages) registers no library directory with the loader;
# its libraries find each other through DT_RPATH
# ($ORIGIN/rocm_sysdeps/lib:$ORIGIN/llvm/lib:$ORIGIN, plus $ORIGIN/host-math/lib).
# The sweep runs ldd on the copies in the target dir, where $ORIGIN no longer
# points into /opt/rocm, so the hipblas audio-cpp build failed its package
# validation with "unresolved dependency ... librocm_kpack.so.0",
# librocm_sysdeps_z.so.1, libLLVM.so.24.0git and friends.
#
# The test fabricates both layouts with real ELF objects (gcc + ldd, no GPU):
#   - ROCm 10.x: the missing deps must be bundled, and the fixture must
#     reproduce the failure when the extra search path is left out.
#   - ROCm 7.x: the extra search path is empty, so the sweep is unchanged.
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
SCRIPT="$CURDIR/package-gpu-libs.sh"

if ! command -v gcc >/dev/null 2>&1 || ! command -v ldd >/dev/null 2>&1; then
    echo "SKIP: gcc/ldd not available"
    exit 0
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# so <dir> <soname> [gcc link args...]: build a shared object named <soname>.
# --no-as-needed keeps every -l as DT_NEEDED even though nothing calls into it.
so() {
    local dir="$1" name="$2"
    shift 2
    mkdir -p "$dir"
    echo "int ${name//[^a-zA-Z0-9]/_}_fn(void){return 0;}" > "$WORK/src.c"
    gcc -shared -fPIC -o "$dir/$name" -Wl,-soname,"$name" -Wl,--no-as-needed "$WORK/src.c" "$@"
}

# --- ROCm 10.x layout: /opt/rocm/core-10.1/lib, /opt/rocm/lib -> core-10.1/lib.
R10="$WORK/r10/opt/rocm"
CORE="$R10/core-10.1/lib"
so "$CORE/rocm_sysdeps/lib" librocm_sysdeps_z.so.1
so "$CORE/llvm/lib" libLLVM.so.24.0git -L"$CORE/rocm_sysdeps/lib" -l:librocm_sysdeps_z.so.1 \
    -Wl,--disable-new-dtags -Wl,-rpath,'$ORIGIN/../../rocm_sysdeps/lib'
so "$CORE/host-math/lib" libfakehostmath.so.3
so "$CORE" librocm_kpack.so.0
# The allowlisted library, linked the way TheRock links libamdhip64/libhipblaslt:
# DT_RPATH (not RUNPATH) relative to $ORIGIN.
so "$CORE" libamdhip64.so.7 \
    -L"$CORE" -l:librocm_kpack.so.0 \
    -L"$CORE/rocm_sysdeps/lib" -l:librocm_sysdeps_z.so.1 \
    -L"$CORE/llvm/lib" -l:libLLVM.so.24.0git \
    -L"$CORE/host-math/lib" -l:libfakehostmath.so.3 \
    -Wl,--disable-new-dtags \
    -Wl,-rpath,'$ORIGIN/rocm_sysdeps/lib:$ORIGIN/llvm/lib:$ORIGIN:$ORIGIN/host-math/lib'
ln -s core-10.1/lib "$R10/lib"

# shellcheck source=/dev/null
source "$SCRIPT" "$WORK/unused"

export ROCM_BASE_DIRS="$R10"
search=$(rocm_dep_search_path)
for want in "$R10/lib" "$R10/lib/rocm_sysdeps/lib" "$R10/lib/llvm/lib" "$R10/lib/host-math/lib"; do
    case ":$search:" in
        *":$want:"*) ;;
        *) echo "FAIL: ROCm 10.x search path lacks $want (got '$search')"; exit 1 ;;
    esac
done

# The fixture must reproduce the CI failure without the search path; otherwise
# the assertion below would pass for the wrong reason.
TARGET_LIB_DIR="$WORK/t10-old"
mkdir -p "$TARGET_LIB_DIR"
COPIED_FILES=()
cp "$CORE/libamdhip64.so.7" "$TARGET_LIB_DIR/"
sweep_transitive_deps "$TARGET_LIB_DIR"
if [ -e "$TARGET_LIB_DIR/librocm_kpack.so.0" ]; then
    echo "FAIL: fixture does not reproduce the bug: the build host resolves the deps on its own"
    exit 1
fi

# The real packager path: package_rocm_libs hands rocm_dep_search_path to the
# sweep. Its allowlist globs are fixed to /opt/rocm, so the allowlisted library
# is placed in the target first, as the existing sweep test does. On a host with
# a real ROCm install that allowlist would copy gigabytes of real libraries, so
# there the sweep is called the way package_rocm_libs calls it.
run_rocm_packager() {
    if [ -e /opt/rocm ]; then
        sweep_transitive_deps "$TARGET_LIB_DIR" "$(rocm_dep_search_path)" >/dev/null
    else
        package_rocm_libs >/dev/null
    fi
}
TARGET_LIB_DIR="$WORK/t10"
mkdir -p "$TARGET_LIB_DIR"
COPIED_FILES=()
cp "$CORE/libamdhip64.so.7" "$TARGET_LIB_DIR/"
export BUILD_TYPE=hipblas
run_rocm_packager

fail=false
for dep in librocm_kpack.so.0 librocm_sysdeps_z.so.1 libLLVM.so.24.0git libfakehostmath.so.3; do
    if [ ! -e "$TARGET_LIB_DIR/$dep" ]; then
        echo "FAIL: ROCm 10.x dependency $dep was NOT bundled"
        fail=true
    fi
done
# Same check the backend package.sh runs: resolve everything from lib/ alone.
if LD_LIBRARY_PATH="$TARGET_LIB_DIR" ldd "$TARGET_LIB_DIR/libamdhip64.so.7" | grep -q 'not found'; then
    echo "FAIL: bundled libamdhip64.so.7 still has unresolved dependencies:"
    LD_LIBRARY_PATH="$TARGET_LIB_DIR" ldd "$TARGET_LIB_DIR/libamdhip64.so.7"
    fail=true
fi
if [ "$fail" = true ]; then
    ls -la "$TARGET_LIB_DIR"
    exit 1
fi

# --- Counter-check, ROCm 7.x layout: /opt/rocm-7.2.1/lib with lib/llvm and
# $ORIGIN-only RPATH. No rocm_sysdeps dir, so the search path must be empty and
# the sweep must not start bundling from ROCm's llvm/lib.
R7="$WORK/r7/opt/rocm-7.2.1"
so "$R7/llvm/lib" libLLVM.so.20
mkdir -p "$R7/lib"
ln -s ../llvm "$R7/lib/llvm"
so "$R7/lib" libhipblas.so.3 -L"$R7/llvm/lib" -l:libLLVM.so.20 \
    -Wl,--disable-new-dtags -Wl,-rpath,'$ORIGIN'

export ROCM_BASE_DIRS="$R7"
search7=$(rocm_dep_search_path)
if [ -n "$search7" ]; then
    echo "FAIL: ROCm 7.x layout got a non-empty search path: '$search7'"
    exit 1
fi

TARGET_LIB_DIR="$WORK/t7"
mkdir -p "$TARGET_LIB_DIR"
COPIED_FILES=()
cp "$R7/lib/libhipblas.so.3" "$TARGET_LIB_DIR/"
run_rocm_packager
if [ -e "$TARGET_LIB_DIR/libLLVM.so.20" ]; then
    echo "FAIL: ROCm 7.x sweep changed: it bundled libLLVM from the ROCm llvm/lib dir"
    exit 1
fi

echo "PASS: ROCm 10.x RPATH-only deps are bundled; ROCm 7.x sweep is unchanged"
