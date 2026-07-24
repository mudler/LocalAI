#!/bin/bash
# Regression test for scripts/build/package-gpu-libs.sh Intel oneAPI/SYCL bundling.
#
# Guards the Intel SYCL backend shipping an incomplete oneAPI runtime. Two gaps
# left the backend unusable on any host that does not already carry oneAPI on its
# library path (i.e. everywhere outside the build container):
#
#   1. The backend binaries link several oneAPI libs DIRECTLY (e.g.
#      libmkl_intel_ilp64, libmkl_sycl_blas, libdnnl). The old packager swept the
#      transitive deps of already-bundled libs but never the binaries' own deps,
#      so these were missed -> "libdnnl.so.3: cannot open shared object file".
#   2. The SYCL Unified Runtime dlopens libur_adapter_level_zero.so.0; no ldd
#      sweep can see a dlopen, so the adapter was never bundled -> SYCL finds no
#      Level Zero backend and the model load SIGSEGVs.
#
# This test fabricates a fake oneAPI lib dir, a fake backend binary that links a
# direct dep, and an unlinked (dlopen stand-in) UR adapter, runs the real
# package_intel_libs, and asserts BOTH get bundled.
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
SCRIPT="$CURDIR/package-gpu-libs.sh"

if ! command -v gcc >/dev/null 2>&1 || ! command -v ldd >/dev/null 2>&1; then
    echo "SKIP: gcc/ldd not available"
    exit 0
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Fake oneAPI lib dir (stand-in for /opt/intel/oneapi/.../lib).
ONEAPI="$WORK/oneapi/lib"
mkdir -p "$ONEAPI"

# A direct binary dependency (stand-in for libmkl_intel_ilp64.so.2): the backend
# binary links it, and it must NOT be reachable from anything already bundled.
echo 'int direct_fn(void){return 1;}' > "$WORK/direct.c"
gcc -shared -fPIC -o "$ONEAPI/libfakeoneapidirect.so.2" "$WORK/direct.c"

# A dlopen-only adapter (stand-in for libur_adapter_level_zero.so.0): nothing
# links it, so only the allowlist glob can bundle it.
echo 'int adapter_fn(void){return 2;}' > "$WORK/adapter.c"
gcc -shared -fPIC -o "$ONEAPI/libur_adapter_level_zero.so.0" "$WORK/adapter.c"

# Fake backend binary that links the direct dep, placed where package.sh puts the
# real binaries: package/ , the parent of package/lib.
PKG="$WORK/package"
TARGET="$PKG/lib"
mkdir -p "$TARGET"
echo 'int direct_fn(void); int main(void){return direct_fn();}' > "$WORK/main.c"
gcc -o "$PKG/llama-cpp-grpc" "$WORK/main.c" \
    -L"$ONEAPI" -l:libfakeoneapidirect.so.2 -Wl,-rpath,"$ONEAPI"

# Fake Intel GPU userspace DRIVER dir (stand-in for /usr/lib/x86_64-linux-gnu
# where intel-compute-runtime installs the driver). The driver is dlopen'd by the
# Level Zero / OpenCL loaders at runtime, not linked, so it must be bundled
# explicitly for the backend to run on a host without a matching (or
# glibc-compatible) Intel driver installed.
DRV="$WORK/driver"
mkdir -p "$DRV/intel-opencl"
echo 'int ze_drv(void){return 3;}' > "$WORK/zedrv.c"
gcc -shared -fPIC -o "$DRV/libze_intel_gpu.so.1" "$WORK/zedrv.c"
echo 'int cl_drv(void){return 4;}' > "$WORK/cldrv.c"
gcc -shared -fPIC -o "$DRV/intel-opencl/libigdrcl.so" "$WORK/cldrv.c"

# Fake OpenCL ICD manifest pointing at the driver by an absolute host path (as
# the real intel-opencl package ships it); the packager must copy it and rewrite
# the path to a bare soname so the bundled driver resolves via LD_LIBRARY_PATH.
FAKE_ICD_DIR="$WORK/OpenCL/vendors"
mkdir -p "$FAKE_ICD_DIR"
echo "$DRV/intel-opencl/libigdrcl.so" > "$FAKE_ICD_DIR/intel.icd"

# Make the fake oneAPI resolvable like the real one is on the build image's path.
export LD_LIBRARY_PATH="$ONEAPI:${LD_LIBRARY_PATH:-}"

# shellcheck source=/dev/null
source "$SCRIPT" "$TARGET"

export BUILD_TYPE=sycl_f16
export INTEL_ONEAPI_LIB_DIRS="$ONEAPI"
export INTEL_DRIVER_LIB_DIRS="$DRV $DRV/intel-opencl"
export INTEL_OPENCL_VENDORS_DIR="$FAKE_ICD_DIR"
package_intel_libs

fail=false
if [ ! -e "$TARGET/libfakeoneapidirect.so.2" ]; then
    echo "FAIL: direct binary dependency was NOT bundled (binaries' own deps unswept)"
    fail=true
fi
if [ ! -e "$TARGET/libur_adapter_level_zero.so.0" ]; then
    echo "FAIL: dlopen'd UR Level Zero adapter was NOT bundled"
    fail=true
fi
if [ ! -e "$TARGET/libze_intel_gpu.so.1" ]; then
    echo "FAIL: Level Zero GPU driver (libze_intel_gpu) was NOT bundled"
    fail=true
fi
if [ ! -e "$TARGET/libigdrcl.so" ]; then
    echo "FAIL: OpenCL GPU driver (libigdrcl) was NOT bundled"
    fail=true
fi
# The ICD manifest must be bundled with a bare-soname library_path so the loader
# finds the bundled driver via LD_LIBRARY_PATH.
ICD_OUT="$TARGET/../etc/OpenCL/vendors/intel.icd"
if [ ! -e "$ICD_OUT" ]; then
    echo "FAIL: OpenCL ICD manifest was NOT bundled"
    fail=true
elif grep -q '/' "$ICD_OUT"; then
    echo "FAIL: ICD manifest library_path was not rewritten to a bare soname: $(cat "$ICD_OUT")"
    fail=true
fi

if [ "$fail" = true ]; then
    ls -la "$TARGET" || true
    exit 1
fi

echo "PASS: oneAPI runtime, UR adapter, GPU driver and rewritten ICD were all bundled"
exit 0
