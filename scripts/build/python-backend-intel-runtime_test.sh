#!/bin/bash
# Exercise the real diffusers launcher with versioned ELF libraries, no torch,
# downloads, venv creation or GPU required.
set -euo pipefail

if [ "$(uname -s)" != "Linux" ]; then
    echo "SKIP: Intel Python runtime regression requires Linux ELF loading"
    exit 0
fi
for tool in gcc ldd python3; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "SKIP: $tool not available"
        exit 0
    fi
done
if ! ldd --version 2>&1 | grep -qi 'glibc\|GNU libc'; then
    echo "SKIP: versioned ELF regression requires glibc"
    exit 0
fi

REPO_ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
export TEST_SYSTEM_PYTHON
TEST_SYSTEM_PYTHON=$(command -v python3)
unset ENV_DIR BACKEND_FILE VIRTUAL_ENV PYTHONHOME LD_LIBRARY_PATH
export PORTABLE_PYTHON=false BUILD_TYPE=cpu

# Fail immediately if startup attempts to provision anything.
mkdir -p "$WORK/bin" "$WORK/good" "$WORK/bad" "$WORK/host" "$WORK/cwd"
for tool in uv pip pip3 curl wget; do
    printf '#!/bin/bash\necho "Unexpected provisioning command" >&2\nexit 99\n' > "$WORK/bin/$tool"
    chmod +x "$WORK/bin/$tool"
done
export PATH="$WORK/bin:$PATH"

cat > "$WORK/loader.c" <<'C'
int urFixtureOrigin(void) { return ORIGIN; }
#ifdef MATCHING
int urDeviceWaitExp(void) { return 42; }
#else
int urOldFunction(void) { return 0; }
#endif
C
cat > "$WORK/loader.map" <<'MAP'
LIBUR_LOADER_0.12 { global: ur*; local: *; };
MAP
cat > "$WORK/sycl.c" <<'C'
int urDeviceWaitExp(void);
int syclFixture(void) { return urDeviceWaitExp(); }
C
echo 'int adapterFixture(void) { return ORIGIN; }' > "$WORK/adapter.c"

# All loaders have the same SONAME and version namespace, but only the venv
# copy exports the required versioned symbol (the reported failure class).
for kind in good bad host; do
    flags=()
    case "$kind" in
        good) flags=(-DMATCHING -DORIGIN=1) ;;
        bad) flags=(-DORIGIN=2) ;;
        host) flags=(-DORIGIN=3) ;;
    esac
    gcc -shared -fPIC "${flags[@]}" "$WORK/loader.c" \
        -Wl,-soname,libur_loader.so.0 -Wl,--version-script,"$WORK/loader.map" \
        -o "$WORK/$kind/libur_loader.so.0"
    gcc -shared -fPIC "${flags[@]}" "$WORK/adapter.c" \
        -o "$WORK/$kind/libur_adapter_level_zero.so.0"
done
# The ELF loader expands $ORIGIN relative to the loaded library.
# shellcheck disable=SC2016
gcc -shared -fPIC "$WORK/sycl.c" -L"$WORK/good" -l:libur_loader.so.0 \
    -Wl,-soname,libsycl.so.9 -Wl,-rpath,'$ORIGIN' \
    -o "$WORK/good/libsycl.so.9"

cat > "$WORK/backend.py" <<'PY'
import ctypes
import os
import sys

assert sys.argv[1:] == ["--fixture"], sys.argv
assert os.environ.get("LD_LIBRARY_PATH") == os.environ.get("EXPECTED_PATH"), (
    os.environ.get("LD_LIBRARY_PATH"), os.environ.get("EXPECTED_PATH")
)
if os.environ.get("LOAD_SYCL") == "1":
    sycl = ctypes.CDLL(os.path.join(os.environ["VIRTUAL_ENV"], "lib", "libsycl.so.9"))
    assert sycl.syclFixture() == 42
if "EXPECTED_ORIGIN" in os.environ:
    expected = int(os.environ["EXPECTED_ORIGIN"])
    loader = ctypes.CDLL("libur_loader.so.0")
    assert loader.urFixtureOrigin() == expected
    adapter = ctypes.CDLL("libur_adapter_level_zero.so.0")
    assert adapter.adapterFixture() == expected
print("fixture passed")
PY

make_backend() {
    local dir="$1" env_dir="${2:-$1}"
    mkdir -p "$dir/common" "$env_dir/venv/bin" "$env_dir/venv/lib" "$env_dir/lib"
    cp "$REPO_ROOT/backend/python/diffusers/run.sh" "$dir/run.sh"
    cp "$REPO_ROOT/backend/python/common/libbackend.sh" "$dir/common/libbackend.sh"
    cp "$WORK/backend.py" "$dir/backend.py"
    cat > "$env_dir/venv/bin/python" <<'SH'
#!/bin/bash
exec "$TEST_SYSTEM_PYTHON" "$@"
SH
    chmod +x "$env_dir/venv/bin/python"
    cp "$WORK/good/"* "$env_dir/venv/lib/"
    cp "$WORK/bad/"* "$env_dir/lib/"
}

launch() {
    (cd "$WORK/cwd"; bash "$1/run.sh" --fixture)
}

expect_failure() {
    if "$@" > "$WORK/failure.log" 2>&1; then
        echo "FAIL: incompatible loader unexpectedly succeeded"
        exit 1
    fi
    # A path assertion or launcher error must not count as the expected failure.
    if ! grep -q 'undefined symbol: urDeviceWaitExp, version LIBUR_LOADER_0.12' "$WORK/failure.log"; then
        cat "$WORK/failure.log"
        exit 1
    fi
}

backend="$WORK/built/diffusers"
make_backend "$backend"
mv "$WORK/built" "$WORK/relocated"
backend="$WORK/relocated/diffusers"
export LOAD_SYCL=1 EXPECTED_ORIGIN=1

# Reproduce the old ordering using the same venv Python shim and backend file.
export LD_LIBRARY_PATH="$backend/lib:$WORK/host"
export EXPECTED_PATH="$LD_LIBRARY_PATH" VIRTUAL_ENV="$backend/venv"
expect_failure "$backend/venv/bin/python" "$backend/backend.py" --fixture
unset VIRTUAL_ENV

# Both inherited and packaged conflicts lose to the venv runtime.
export LD_LIBRARY_PATH="$WORK/host" EXPECTED_PATH="$backend/venv/lib:$backend/lib:$WORK/host"
launch "$backend"

# Every startBackend exec branch must receive the same search path.
export BACKEND_FILE="$backend/backend.py"
launch "$backend"
unset BACKEND_FILE
mv "$backend/backend.py" "$backend/server.py"
launch "$backend"
# BACKEND_NAME currently derives from the caller's working directory.
mv "$backend/server.py" "$backend/cwd.py"
launch "$backend"
mv "$backend/cwd.py" "$backend/backend.py"

unset LD_LIBRARY_PATH
export EXPECTED_PATH="$backend/venv/lib:$backend/lib:"
launch "$backend"

# No packaged directory: the venv still takes precedence, with no empty entry
# introduced when the inherited path is unset.
mv "$backend/lib" "$backend/packaged"
export EXPECTED_PATH="$backend/venv/lib"
launch "$backend"
export LD_LIBRARY_PATH="$WORK/host" EXPECTED_PATH="$backend/venv/lib:$WORK/host"
launch "$backend"
mv "$backend/packaged" "$backend/lib"

# The launcher accepts a relative path when its parent directory has spaces;
# its existing unquoted $0 handling does not accept absolute paths with spaces.
spaced="$WORK/relocated with spaces"
make_backend "$spaced/diffusers"
mkdir -p "$spaced/cwd"
export EXPECTED_PATH="$spaced/diffusers/venv/lib:$spaced/diffusers/lib:$WORK/host"
(cd "$spaced/cwd"; bash ../diffusers/run.sh --fixture)

separate="$WORK/separate environment"
make_backend "$WORK/external" "$separate"
export ENV_DIR="$separate" EXPECTED_PATH="$separate/venv/lib:$separate/lib:$WORK/host"
launch "$WORK/external"
unset ENV_DIR

# Missing and incompatible venv loaders must still fail, even though the venv
# directory and SYCL library exist. Presence is not an ABI compatibility check.
export EXPECTED_PATH="$backend/venv/lib:$backend/lib:$WORK/host"
rm "$backend/venv/lib/libur_loader.so.0"
expect_failure launch "$backend"
cp "$WORK/bad/libur_loader.so.0" "$backend/venv/lib/"
expect_failure launch "$backend"

# Without a SYCL library, unrelated CPU/CUDA files (or even a directory named
# libsycl.so) must not change the existing library search order.
rm "$backend/venv/lib/libsycl.so.9"
mkdir "$backend/venv/lib/libsycl.so"
touch "$backend/venv/lib/libcublas.so.12" "$backend/venv/lib/libcpu.so"
export LOAD_SYCL=0 EXPECTED_ORIGIN=2 EXPECTED_PATH="$backend/lib:$WORK/host"
launch "$backend"
unset LD_LIBRARY_PATH
export EXPECTED_PATH="$backend/lib:"
launch "$backend"
rm -rf "${backend:?}/lib"
export LD_LIBRARY_PATH="$WORK/host" EXPECTED_PATH="$WORK/host" EXPECTED_ORIGIN=3
launch "$backend"
unset LD_LIBRARY_PATH EXPECTED_PATH EXPECTED_ORIGIN
launch "$backend"

echo "PASS: Intel Python runtime precedence, relocation, fallback and import errors"
