#!/bin/bash
# Checks how the run.sh of each C++ backend sets up the Intel graphics driver.
#
# A backend built for Intel GPUs carries its own copy of the Intel graphics
# driver. run.sh has to tell Level Zero, which is how llama.cpp reaches the
# card, to use that copy. Three things must hold, and all three have broken in
# the past:
#
#   1. If the user already chose a driver, keep the user's choice. Otherwise a
#      machine with a graphics card too new for the carried driver stops
#      working, with no way to get back to the driver that did work.
#   2. Say nothing about OpenCL. No OpenCL driver is carried, so pointing
#      OpenCL at the backend's own directory would leave it with no driver at
#      all, where saying nothing leaves it the machine's own.
#   3. Ask the driver for the amount of free memory. Without this, llama.cpp
#      reads zero free memory on an integrated graphics chip, because such a
#      chip has no memory of its own and shares the system's.
#
# The test builds a fake backend directory for each run.sh, runs it, and reads
# back the variables it exported.
set -euo pipefail

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

REPO_ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")

RUN_SCRIPTS=(
    "backend/cpp/llama-cpp/run.sh llama-cpp"
    "backend/cpp/turboquant/run.sh turboquant"
    "backend/cpp/bonsai/run.sh bonsai"
)

failures=0

fail() {
    echo "FAIL: $*"
    failures=$((failures + 1))
}

# Builds a fake backend directory: the real run.sh, a stand-in for the backend
# program that prints the variables we care about, and whichever libraries the
# caller asked for.
#
# Usage: make_backend <dir> <program-prefix> [library ...]
make_backend() {
    local dir="$1" prefix="$2"
    shift 2

    mkdir -p "$dir/lib"
    cp "$RUN_SH" "$dir/run.sh"
    chmod +x "$dir/run.sh"

    local lib
    for lib in "$@"; do
        : > "$dir/lib/$lib"
    done

    cat > "$dir/${prefix}-fallback" <<'PROGRAM'
#!/bin/bash
echo "level_zero_driver=${ZE_ENABLE_ALT_DRIVERS:-}"
echo "opencl_driver_list=${OCL_ICD_VENDORS:-}"
echo "report_free_memory=${ZES_ENABLE_SYSMAN:-}"
PROGRAM
    chmod +x "$dir/${prefix}-fallback"
}

# Runs a fake backend and prints the one variable asked for.
# Usage: read_variable <dir> <name>
read_variable() {
    local dir="$1" name="$2"
    bash "$dir/run.sh" 2>/dev/null | sed -n "s/^${name}=//p"
}

for entry in "${RUN_SCRIPTS[@]}"; do
    read -r script prefix <<< "$entry"
    RUN_SH="$REPO_ROOT/$script"

    if [ ! -f "$RUN_SH" ]; then
        fail "$script does not exist"
        continue
    fi

    # An Intel build with its own graphics driver: point Level Zero and OpenCL
    # at the bundled copies and ask for the free memory reading.
    bundled="$WORK/$prefix-bundled"
    make_backend "$bundled" "$prefix" \
        libze_loader.so.1 libze_intel_gpu.so.1 libigdrcl.so
    mkdir -p "$bundled/etc/OpenCL/vendors"
    echo "libigdrcl.so" > "$bundled/etc/OpenCL/vendors/intel.icd"

    got=$(read_variable "$bundled" level_zero_driver)
    if [ "$got" != "$bundled/lib/libze_intel_gpu.so.1" ]; then
        fail "$script: expected Level Zero to use the bundled driver, got '$got'"
    fi

    # Even with an OpenCL driver and a driver list sitting in the backend, which
    # is what an older packaging left behind, OpenCL must be left alone.
    got=$(read_variable "$bundled" opencl_driver_list)
    if [ -n "$got" ]; then
        fail "$script: OpenCL was pointed at the backend's own directory ('$got')"
    fi

    got=$(read_variable "$bundled" report_free_memory)
    if [ "$got" != "1" ]; then
        fail "$script: expected the free memory reading to be turned on, got '$got'"
    fi

    # The user picked a driver already. Both choices must survive.
    got=$(ZE_ENABLE_ALT_DRIVERS=/usr/lib/host-driver.so \
        read_variable "$bundled" level_zero_driver)
    if [ "$got" != "/usr/lib/host-driver.so" ]; then
        fail "$script: the user's Level Zero driver was overwritten with '$got'"
    fi

    got=$(ZES_ENABLE_SYSMAN=0 read_variable "$bundled" report_free_memory)
    if [ "$got" != "0" ]; then
        fail "$script: the user's free memory setting was overwritten with '$got'"
    fi

    # A build for some other kind of graphics card. None of the Intel
    # variables belong here.
    other="$WORK/$prefix-other"
    make_backend "$other" "$prefix" libcublas.so.12

    for name in level_zero_driver opencl_driver_list report_free_memory; do
        got=$(read_variable "$other" "$name")
        if [ -n "$got" ]; then
            fail "$script: $name was set on a build with no Intel libraries ('$got')"
        fi
    done
done

if [ "$failures" -gt 0 ]; then
    echo "$failures check(s) failed"
    exit 1
fi

echo "PASS: every run.sh sets up the Intel graphics driver correctly"
