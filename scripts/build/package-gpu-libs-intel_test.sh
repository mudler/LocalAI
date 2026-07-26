#!/bin/bash
# Checks what package_intel_libs puts in a backend built for Intel GPUs.
#
# The packager copies the libraries a backend needs next to the backend itself,
# so it can run on a machine that has none of them installed. Four things have
# to happen, and each one has been missing at some point:
#
#   1. Copy the libraries the backend program is linked against. Some of them
#      are only reachable from the program, not from any other copied library,
#      so looking at the copied libraries alone is not enough.
#   2. Copy the libraries that are opened by name while the program runs. Those
#      are invisible to any tool that reads the list of libraries a file is
#      linked against, so they have to be named one by one.
#   3. Copy the Intel graphics driver, which is also opened by name at run
#      time, together with the small text file that tells OpenCL where the
#      driver is. Rewrite that file to hold a plain file name, because the
#      original path only exists on the machine that did the build.
#   4. Leave out the text file for any driver that was not copied. Keeping it
#      would send OpenCL looking for a file that is not there.
#
# The test builds a stand-in for an oneAPI installation, a stand-in for a
# driver installation and two fake backend programs, runs the real packager and
# checks the result.
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
SCRIPT="$CURDIR/package-gpu-libs.sh"

if ! command -v gcc >/dev/null 2>&1 || ! command -v ldd >/dev/null 2>&1; then
    echo "SKIP: gcc/ldd not available"
    exit 0
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Stand-in for /opt/intel/oneapi/*/lib.
ONEAPI="$WORK/oneapi/lib"
mkdir -p "$ONEAPI"

# Two libraries the backend programs are linked against, one each. Nothing else
# refers to them, so they can only be found by looking at the programs.
echo 'int first_fn(void){return 1;}' > "$WORK/first.c"
gcc -shared -fPIC -o "$ONEAPI/libfakeoneapifirst.so.2" "$WORK/first.c"
echo 'int second_fn(void){return 2;}' > "$WORK/second.c"
gcc -shared -fPIC -o "$ONEAPI/libfakeoneapisecond.so.2" "$WORK/second.c"

# A library that is opened by name while the program runs. Nothing is linked
# against it, so only the list of names in the packager can find it.
echo 'int adapter_fn(void){return 3;}' > "$WORK/adapter.c"
gcc -shared -fPIC -o "$ONEAPI/libur_adapter_level_zero.so.0" "$WORK/adapter.c"

# Two fake backend programs, in the directory the real packaging script uses:
# package/, one level above package/lib. One is named after llama.cpp, the
# other is not, because the same packager serves several backends.
PKG="$WORK/package"
TARGET="$PKG/lib"
mkdir -p "$TARGET"
echo 'int first_fn(void); int main(void){return first_fn();}' > "$WORK/main1.c"
gcc -o "$PKG/llama-cpp-grpc" "$WORK/main1.c" \
    -L"$ONEAPI" -l:libfakeoneapifirst.so.2 -Wl,-rpath,"$ONEAPI"
echo 'int second_fn(void); int main(void){return second_fn();}' > "$WORK/main2.c"
gcc -o "$PKG/bonsai-grpc" "$WORK/main2.c" \
    -L"$ONEAPI" -l:libfakeoneapisecond.so.2 -Wl,-rpath,"$ONEAPI"

# The real directory also holds the script that starts the backend. Looking at a
# shell script for libraries has to be harmless.
printf '#!/bin/bash\necho started\n' > "$PKG/run.sh"
chmod +x "$PKG/run.sh"

# Stand-in for the Intel graphics driver installation. Both files are opened by
# name at run time rather than linked, so the packager has to name them.
DRV="$WORK/driver"
mkdir -p "$DRV/intel-opencl"
echo 'int ze_drv(void){return 4;}' > "$WORK/zedrv.c"
gcc -shared -fPIC -o "$DRV/libze_intel_gpu.so.1" "$WORK/zedrv.c"
echo 'int cl_drv(void){return 5;}' > "$WORK/cldrv.c"
gcc -shared -fPIC -o "$DRV/intel-opencl/libigdrcl.so" "$WORK/cldrv.c"

# Stand-in for /etc/OpenCL/vendors. One file names the graphics driver by its
# path on the build machine. The other names a library that is not part of the
# driver, the way the oneAPI images list their processor-only OpenCL support.
VENDORS="$WORK/OpenCL/vendors"
mkdir -p "$VENDORS"
echo "$DRV/intel-opencl/libigdrcl.so" > "$VENDORS/intel_gpu.icd"
echo "libintelocl.so" > "$VENDORS/intel_cpu.icd"

# Let the fake oneAPI libraries be found the way the real ones are on the build
# machine.
export LD_LIBRARY_PATH="$ONEAPI:${LD_LIBRARY_PATH:-}"

# shellcheck source=/dev/null
source "$SCRIPT" "$TARGET"

export BUILD_TYPE=sycl_f16
export INTEL_ONEAPI_LIB_DIRS="$ONEAPI"
export INTEL_DRIVER_LIB_DIRS="$DRV $DRV/intel-opencl"
export INTEL_OPENCL_VENDORS_DIR="$VENDORS"
package_intel_libs

fail=false

for lib in libfakeoneapifirst.so.2 libfakeoneapisecond.so.2; do
    if [ ! -e "$TARGET/$lib" ]; then
        echo "FAIL: $lib is missing; the backend programs' own libraries were not copied"
        fail=true
    fi
done

if [ ! -e "$TARGET/libur_adapter_level_zero.so.0" ]; then
    echo "FAIL: the Level Zero adapter, which is opened by name, was not copied"
    fail=true
fi

for lib in libze_intel_gpu.so.1 libigdrcl.so; do
    if [ ! -e "$TARGET/$lib" ]; then
        echo "FAIL: the graphics driver file $lib was not copied"
        fail=true
    fi
done

# The file for the copied driver must be there and must hold a plain file name.
GPU_ICD="$TARGET/../etc/OpenCL/vendors/intel_gpu.icd"
if [ ! -e "$GPU_ICD" ]; then
    echo "FAIL: the OpenCL file naming the graphics driver was not copied"
    fail=true
elif grep -q '/' "$GPU_ICD"; then
    echo "FAIL: the OpenCL file still holds a path: $(cat "$GPU_ICD")"
    fail=true
fi

# The file for the library that was not copied must be left out.
if [ -e "$TARGET/../etc/OpenCL/vendors/intel_cpu.icd" ]; then
    echo "FAIL: an OpenCL file was kept for a library that was not copied"
    fail=true
fi

# The Python backends for Intel GPUs, built as BUILD_TYPE=intel, start without
# run.sh and so never load a copied driver. Copying one for them would add
# several hundred megabytes that nothing reads.
PYTHON_STYLE="$WORK/python-backend/lib"
mkdir -p "$PYTHON_STYLE"
(
    BUILD_TYPE=intel \
    INTEL_ONEAPI_LIB_DIRS="$ONEAPI" \
    INTEL_DRIVER_LIB_DIRS="$DRV $DRV/intel-opencl" \
    INTEL_OPENCL_VENDORS_DIR="$VENDORS" \
    bash -c 'source "$0" "$1"; package_intel_libs' "$SCRIPT" "$PYTHON_STYLE"
) >/dev/null 2>&1
if [ -e "$PYTHON_STYLE/libze_intel_gpu.so.1" ]; then
    echo "FAIL: the graphics driver was copied into a backend that cannot load it"
    fail=true
fi

# A build for Intel GPUs that ends up with no driver still works, but only on a
# machine that has its own. That is easy to cause by accident and impossible to
# see afterwards, so the packager has to say so.
warning=$(
    BUILD_TYPE=sycl_f16 \
    INTEL_ONEAPI_LIB_DIRS="$ONEAPI" \
    INTEL_DRIVER_LIB_DIRS="$WORK/empty" \
    INTEL_OPENCL_VENDORS_DIR="$WORK/empty" \
    bash -c 'source "$0" "$1"; package_intel_libs' \
        "$SCRIPT" "$WORK/nodriver/lib" 2>&1 >/dev/null || true
)
if ! grep -qi "no intel graphics driver" <<< "$warning"; then
    echo "FAIL: no warning when the graphics driver could not be copied"
    fail=true
fi

if [ "$fail" = true ]; then
    ls -la "$TARGET" || true
    exit 1
fi

echo "PASS: the oneAPI libraries, the adapter, the graphics driver and its OpenCL file were all handled"
exit 0
