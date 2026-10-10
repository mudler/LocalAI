#!/bin/bash
# Regression test for backend/python/vllm-omni/install.sh.
#
# Guards issue #9162: an editable (`pip install -e .`) install of vllm-omni
# records the builder source path in the package finder. After the backend is
# copied out of the image into a renamed directory, `import vllm_omni` fails
# with ModuleNotFoundError. A regular install copies the package into the venv
# site-packages, so imports survive relocation.
#
# Drives the real installer with a fixture libbackend.sh so requirements
# installation and protobuf generation are not executed, and with command
# substitutes so an unexpected download fails immediately. The relocatable
# import case delegates only the local package install to a real package
# manager; everything else stays stubbed.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
installer="$repo_root/backend/python/vllm-omni/install.sh"

if [ ! -f "$installer" ]; then
    echo "FAIL: missing $installer" >&2
    exit 1
fi

WORK=$(cd "$(mktemp -d)" && pwd -P)
trap 'rm -rf "$WORK"' EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

assert_file_equals() {
    local want=$1
    local got_file=$2
    local msg=$3
    local got

    got=$(cat "$got_file")
    if [ "$got" != "$want" ]; then
        echo "FAIL: $msg" >&2
        echo "expected:" >&2
        printf '%s\n' "$want" >&2
        echo "got:" >&2
        printf '%s\n' "$got" >&2
        exit 1
    fi
}

mkdir -p "$WORK/bin"

# Any unexpected fetch or clone must fail immediately.
for tool in curl wget pip3; do
    cat > "$WORK/bin/$tool" <<'EOF'
#!/bin/bash
echo "Unexpected provisioning command: $(basename "$0") $*" >&2
exit 99
EOF
    chmod +x "$WORK/bin/$tool"
done

cat > "$WORK/bin/git" <<'EOF'
#!/bin/bash
set -euo pipefail
case "$*" in
    init) cp -R "$SOURCE_FIXTURE/." . ;;
    'fetch --depth 1 https://github.com/vllm-project/vllm-omni.git '*|'checkout --detach FETCH_HEAD'|'tag v'*) ;;
    *) echo "Unexpected git invocation: $*" >&2; exit 99 ;;
esac
EOF
cat > "$WORK/bin/python" <<'EOF'
#!/bin/bash
set -euo pipefail
if [ "${DELEGATE_LOCAL_INSTALL:-}" = 1 ]; then
    exec "$VENV_PYTHON" "$@"
fi
cat >/dev/null
EOF
chmod +x "$WORK/bin/git" "$WORK/bin/python"

cat > "$WORK/bin/pkg-install" <<'EOF'
#!/bin/bash
set -euo pipefail

tool=$(basename "$0")
{
    printf '%s' "$tool"
    for arg in "$@"; do
        printf ' %s' "$arg"
    done
    printf '\n'
} >> "${CMD_LOG}"

if [ "$tool" = "uv" ]; then
    if [ "${1:-}" != "pip" ] || [ "${2:-}" != "install" ]; then
        echo "Unexpected uv invocation: $tool $*" >&2
        exit 99
    fi
    shift 2
elif [ "$tool" = "pip" ]; then
    if [ "${1:-}" != "install" ]; then
        echo "Unexpected pip invocation: $tool $*" >&2
        exit 99
    fi
    shift
else
    echo "Unexpected tool $tool" >&2
    exit 99
fi

is_local=0
is_editable=0
is_vllm=0
for arg in "$@"; do
    case "$arg" in
        -e|--editable) is_editable=1 ;;
        .) is_local=1 ;;
        vllm|vllm==*) is_vllm=1 ;;
    esac
done

if [ "$is_vllm" = 1 ] && [ "$is_local" = 0 ]; then
    echo "install-vllm $*" >> "${EVENT_LOG}"
    exit 0
fi

if [ "$is_local" = 1 ]; then
    echo "install-local $*" >> "${EVENT_LOG}"
    if [ "$is_editable" = 1 ]; then
        echo "editable" >> "${EVENT_LOG}"
    else
        echo "regular" >> "${EVENT_LOG}"
    fi
    if [ "${FAIL_LOCAL_INSTALL:-}" = 1 ]; then
        echo "local package install failed" >&2
        exit 1
    fi
    if [ "${DELEGATE_LOCAL_INSTALL:-}" = 1 ]; then
        if [ -z "${VENV_PYTHON:-}" ]; then
            echo "VENV_PYTHON is required for delegated local install" >&2
            exit 99
        fi
        exec "${VENV_PYTHON}" -m pip install \
            --no-index --no-build-isolation --no-deps \
            --disable-pip-version-check \
            .
    fi
    exit 0
fi

echo "Unexpected install arguments: $*" >&2
exit 99
EOF
chmod +x "$WORK/bin/pkg-install"
ln -s pkg-install "$WORK/bin/pip"
ln -s pkg-install "$WORK/bin/uv"

export PATH="$WORK/bin:$PATH"

write_libbackend_fixture() {
    cat > "$1" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
: "${BUILD_TYPE:=}"
: "${BUILD_PROFILE:=}"
: "${USE_PIP:=false}"
: "${EXTRA_PIP_INSTALL_FLAGS:=}"

installRequirements() {
    echo "installRequirements" >> "${EVENT_LOG}"
}

runProtogen() {
    echo "runProtogen" >> "${EVENT_LOG}"
}
EOF
}

write_fixture_package() {
    local root="$1/vllm-omni"
    mkdir -p "$root/vllm_omni/entrypoints"
    cat > "$root/pyproject.toml" <<'EOF'
[build-system]
requires = ["setuptools"]
build-backend = "setuptools.build_meta"

[project]
name = "vllm-omni"
version = "0.0.0"

[tool.setuptools.packages.find]
include = ["vllm_omni*"]
EOF
    printf '%s\n' '# fixture' > "$root/vllm_omni/__init__.py"
    printf '%s\n' 'fixture: true' > "$root/vllm_omni/stages.yaml"
    printf '%s\n' '# fixture' > "$root/vllm_omni/entrypoints/__init__.py"
    cat > "$root/vllm_omni/entrypoints/omni.py" <<'EOF'
class Omni:
    source = "fixture"
EOF
}

setup_backend_dir() {
    local dir=$1
    mkdir -p "$dir/common" "$dir/vllm-omni"
    cp "$installer" "$dir/install.sh"
    write_libbackend_fixture "$dir/common/libbackend.sh"
    write_fixture_package "$dir"
}

disable_system_site() {
    local cfg="$1/pyvenv.cfg"
    local tmp="$1/pyvenv.cfg.tmp"
    if [ ! -f "$cfg" ]; then
        echo "include-system-site-packages = false" > "$cfg"
        return
    fi
    if grep -q '^include-system-site-packages' "$cfg"; then
        sed 's/^include-system-site-packages.*/include-system-site-packages = false/' "$cfg" > "$tmp"
        mv "$tmp" "$cfg"
    else
        echo "include-system-site-packages = false" >> "$cfg"
    fi
}

run_install() {
    local backend=$1
    (
        cd "$backend"
        BUILD_TYPE="${BUILD_TYPE:-}" \
        BUILD_PROFILE="${BUILD_PROFILE:-}" \
        CUDA_MAJOR_VERSION="${CUDA_MAJOR_VERSION:-}" \
        USE_PIP="${USE_PIP:-false}" \
        EXTRA_PIP_INSTALL_FLAGS="${EXTRA_PIP_INSTALL_FLAGS:-}" \
        EVENT_LOG="$EVENT_LOG" \
        CMD_LOG="$CMD_LOG" \
        FAIL_LOCAL_INSTALL="${FAIL_LOCAL_INSTALL:-}" \
        DELEGATE_LOCAL_INSTALL="${DELEGATE_LOCAL_INSTALL:-}" \
        VENV_PYTHON="${VENV_PYTHON:-}" \
        SOURCE_FIXTURE="$backend/vllm-omni" \
        PIP_NO_INDEX=1 \
        UV_OFFLINE=1 \
        bash ./install.sh
    )
}

run_installer_case() {
    local name=$1
    local use_pip=$2
    local build_type=$3
    local build_profile=$4
    local cuda_major=$5
    local expected_vllm=$6
    local case_dir="$WORK/cases/$name"
    local backend="$case_dir/backend"
    local expected_tool expected_events expected_cmds

    unset DELEGATE_LOCAL_INSTALL FAIL_LOCAL_INSTALL VENV_PYTHON
    mkdir -p "$case_dir"
    setup_backend_dir "$backend"
    : > "$case_dir/events.log"
    : > "$case_dir/commands.log"

    EVENT_LOG="$case_dir/events.log"
    CMD_LOG="$case_dir/commands.log"
    BUILD_TYPE=$build_type
    BUILD_PROFILE=$build_profile
    CUDA_MAJOR_VERSION=$cuda_major
    USE_PIP=$use_pip
    EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"
    export EVENT_LOG CMD_LOG BUILD_TYPE BUILD_PROFILE CUDA_MAJOR_VERSION USE_PIP EXTRA_PIP_INSTALL_FLAGS

    if ! run_install "$backend" >"$case_dir/install.out" 2>&1; then
        cat "$case_dir/install.out" >&2
        fail "install.sh failed for $name"
    fi

    if [ "$use_pip" = true ]; then
        expected_tool=pip
    else
        expected_tool="uv pip"
    fi

    expected_events=$(printf '%s\n' \
        "installRequirements" \
        "install-vllm ${expected_vllm}" \
        "install-local --no-build-isolation . ${expected_vllm}" \
        "regular" \
        "runProtogen")
    expected_cmds=$(printf '%s\n' \
        "${expected_tool} install ${expected_vllm}" \
        "${expected_tool} install --no-build-isolation . ${expected_vllm}")

    assert_file_equals "$expected_events" "$EVENT_LOG" "$name event log"
    assert_file_equals "$expected_cmds" "$CMD_LOG" "$name command log"
}

run_installer_case cuda12-pip true cublas cublas12 12 "vllm==0.14.0"
run_installer_case cuda12-uv false cublas cublas12 12 "vllm==0.14.0 --torch-backend=auto"
run_installer_case cuda13-pip true cublas cublas13 13 "vllm==0.20.0"
run_installer_case cuda13-uv false cublas cublas13 13 "vllm==0.20.0 --torch-backend=auto"

echo "PASS: vllm-omni installer uses a regular local package install (pip/uv, CUDA 12/13)"

# A failed final package install must not regenerate protobuf stubs.
fail_dir="$WORK/cases/local-fail"
fail_backend="$fail_dir/backend"
mkdir -p "$fail_dir"
setup_backend_dir "$fail_backend"
: > "$fail_dir/events.log"
: > "$fail_dir/commands.log"
EVENT_LOG="$fail_dir/events.log"
CMD_LOG="$fail_dir/commands.log"
BUILD_TYPE=cublas
BUILD_PROFILE=cublas12
CUDA_MAJOR_VERSION=12
USE_PIP=true
EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"
FAIL_LOCAL_INSTALL=1
unset DELEGATE_LOCAL_INSTALL VENV_PYTHON
export EVENT_LOG CMD_LOG BUILD_TYPE BUILD_PROFILE CUDA_MAJOR_VERSION USE_PIP EXTRA_PIP_INSTALL_FLAGS FAIL_LOCAL_INSTALL

if run_install "$fail_backend" >"$fail_dir/install.out" 2>&1; then
    cat "$fail_dir/install.out" >&2
    fail "install.sh succeeded when the local package install failed"
fi
if grep -qx runProtogen "$EVENT_LOG"; then
    cat "$EVENT_LOG" >&2
    fail "runProtogen ran after a failed local package install"
fi
if ! grep -qx 'install-local --no-build-isolation . vllm==0.14.0' "$EVENT_LOG"; then
    cat "$EVENT_LOG" >&2
    fail "failed local package install was not attempted"
fi

echo "PASS: failed local package install does not run protogen"

unset FAIL_LOCAL_INSTALL

test_relocatable_import() {
    if ! command -v python3 >/dev/null 2>&1; then
        echo "SKIP: relocatable import regression (python3 not available)"
        return 0
    fi
    if ! python3 -c "import venv, ensurepip, setuptools" 2>/dev/null; then
        echo "SKIP: relocatable import regression (python3 venv, ensurepip, or setuptools not available)"
        return 0
    fi

    local backend relocated ebackend erelocated before after efile eout
    local case_dir="$WORK/import-regular"
    backend="$case_dir/vllm-omni"
    mkdir -p "$case_dir"
    setup_backend_dir "$backend"
    write_fixture_package "$backend"
    python3 -m venv --system-site-packages "$backend/venv"

    : > "$case_dir/events.log"
    : > "$case_dir/commands.log"
    EVENT_LOG="$case_dir/events.log"
    CMD_LOG="$case_dir/commands.log"
    BUILD_TYPE=cublas
    BUILD_PROFILE=cublas12
    CUDA_MAJOR_VERSION=12
    USE_PIP=true
    EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"
    DELEGATE_LOCAL_INSTALL=1
    VENV_PYTHON="$backend/venv/bin/python"
    unset FAIL_LOCAL_INSTALL
    export EVENT_LOG CMD_LOG BUILD_TYPE BUILD_PROFILE CUDA_MAJOR_VERSION USE_PIP EXTRA_PIP_INSTALL_FLAGS DELEGATE_LOCAL_INSTALL VENV_PYTHON

    if ! run_install "$backend" >"$case_dir/install.out" 2>&1; then
        cat "$case_dir/install.out" >&2
        fail "install.sh failed during relocatable import setup"
    fi
    if ! grep -qx regular "$EVENT_LOG"; then
        cat "$EVENT_LOG" >&2
        fail "relocatable import setup did not select a regular local install"
    fi
    if grep -qx editable "$EVENT_LOG"; then
        cat "$EVENT_LOG" >&2
        fail "relocatable import setup selected an editable local install"
    fi
    if ! grep -qx runProtogen "$EVENT_LOG"; then
        cat "$EVENT_LOG" >&2
        fail "relocatable import setup skipped the final protogen regeneration"
    fi

    before=$("$VENV_PYTHON" -c "import pathlib, vllm_omni; print(pathlib.Path(vllm_omni.__file__).resolve())")
    case "$before" in
        *"/site-packages/"*) ;;
        *) fail "regular install did not land in site-packages: $before" ;;
    esac

    relocated="$WORK/import-regular-relocated/vllm-omni"
    mkdir -p "$(dirname "$relocated")"
    mv "$backend" "$relocated"
    rm -rf "$relocated/vllm-omni"
    disable_system_site "$relocated/venv"

    mkdir -p "$WORK/unrelated-cwd"
    if ! (
        cd "$WORK/unrelated-cwd"
        env -u PYTHONPATH PYTHONNOUSERSITE=1 \
            "$relocated/venv/bin/python" -c \
            "from vllm_omni.entrypoints.omni import Omni; raise SystemExit(0 if Omni.source == 'fixture' else 1)"
    ); then
        fail "regular install failed to import vllm_omni after relocation"
    fi

    after=$(
        cd "$WORK/unrelated-cwd"
        env -u PYTHONPATH PYTHONNOUSERSITE=1 \
            "$relocated/venv/bin/python" -c \
            "import pathlib, vllm_omni; print(pathlib.Path(vllm_omni.__file__).resolve())"
    )
    case "$after" in
        "$relocated"/*"/site-packages/"*) ;;
        *) fail "relocated import did not resolve from relocated site-packages: $after" ;;
    esac

    ebackend="$WORK/import-editable/vllm-omni"
    mkdir -p "$ebackend"
    write_fixture_package "$ebackend"
    python3 -m venv --system-site-packages "$ebackend/venv"
    if ! (
        cd "$ebackend/vllm-omni"
        "$ebackend/venv/bin/python" -m pip install \
            --no-index --no-build-isolation --no-deps \
            --disable-pip-version-check -e .
    ) >"$WORK/import-editable/install.out" 2>&1; then
        cat "$WORK/import-editable/install.out" >&2
        fail "editable control install failed"
    fi

    efile=$("$ebackend/venv/bin/python" -c "import pathlib, vllm_omni; print(pathlib.Path(vllm_omni.__file__).resolve())")
    case "$efile" in
        "$ebackend"/vllm-omni/*) ;;
        *) fail "editable control did not point at the source tree: $efile" ;;
    esac

    erelocated="$WORK/import-editable-relocated/vllm-omni"
    mkdir -p "$(dirname "$erelocated")"
    mv "$ebackend" "$erelocated"
    rm -rf "$erelocated/vllm-omni"
    disable_system_site "$erelocated/venv"

    eout=""
    if eout=$(
        cd "$WORK/unrelated-cwd"
        env -u PYTHONPATH PYTHONNOUSERSITE=1 \
            "$erelocated/venv/bin/python" -c "from vllm_omni.entrypoints.omni import Omni" 2>&1
    ); then
        fail "editable control unexpectedly imported after relocation: $eout"
    fi
    case "$eout" in
        *ModuleNotFoundError*|*vllm_omni*) ;;
        *) fail "editable control failed for an unexpected reason: $eout" ;;
    esac

    echo "PASS: relocated regular install imports vllm_omni; editable control fails"
}

test_relocatable_import
