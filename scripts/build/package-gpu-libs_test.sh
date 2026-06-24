#!/bin/bash
# Regression test for scripts/build/package-gpu-libs.sh.
#
# Guards issue #10537: the per-vendor packagers copy an explicit allowlist of
# top-level GPU runtime libs but used to miss their transitive dependencies
# (e.g. ROCm's librocprofiler-register.so.0). Since backends run through the
# bundled lib/ld.so with LD_LIBRARY_PATH=lib, an unbundled transitive dep is a
# fatal "cannot open shared object file" at load time.
#
# This test fabricates a primary lib that links a transitive lib, simulates the
# allowlist step (primary copied, transitive not), and asserts the transitive
# sweep pulls the dependency in. Requires gcc + ldd (present in build images).
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
SCRIPT="$CURDIR/package-gpu-libs.sh"

if ! command -v gcc >/dev/null 2>&1 || ! command -v ldd >/dev/null 2>&1; then
    echo "SKIP: gcc/ldd not available"
    exit 0
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Transitive dependency (stand-in for librocprofiler-register.so.0).
echo 'int transitive_fn(void){return 42;}' > "$WORK/transitive.c"
gcc -shared -fPIC -o "$WORK/libfaketransitive.so.0" "$WORK/transitive.c"

# Primary allowlisted lib (stand-in for libhipblas.so) that links it.
echo 'int transitive_fn(void); int primary_fn(void){return transitive_fn();}' > "$WORK/primary.c"
gcc -shared -fPIC -o "$WORK/libfakeprimary.so.0" "$WORK/primary.c" \
    -L"$WORK" -l:libfaketransitive.so.0 -Wl,-rpath,"$WORK"

# Simulate the allowlist step: primary already bundled, transitive not.
TARGET="$WORK/target"
mkdir -p "$TARGET"
cp "$WORK/libfakeprimary.so.0" "$TARGET/"

# Make the transitive dep resolvable like /opt/rocm libs are in the build image.
export LD_LIBRARY_PATH="$WORK:${LD_LIBRARY_PATH:-}"

# shellcheck source=/dev/null
source "$SCRIPT" "$TARGET"
sweep_transitive_deps "$TARGET"

if [ -e "$TARGET/libfaketransitive.so.0" ]; then
    echo "PASS: transitive dependency was bundled by sweep_transitive_deps"
    exit 0
fi

echo "FAIL: transitive dependency was NOT bundled (regression of #10537)"
ls -la "$TARGET"
exit 1
