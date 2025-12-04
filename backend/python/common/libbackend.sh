#!/usr/bin/env bash
set -euo pipefail

# 
# use the library by adding the following line to a script:
# source $(dirname $0)/../common/libbackend.sh
#
# If you want to limit what targets a backend can be used on, set the variable LIMIT_TARGETS to a
# space separated list of valid targets BEFORE sourcing the library, for example to only allow a backend
# to be used on CUDA and CPU backends:
#
# LIMIT_TARGETS="cublas cpu"
# source $(dirname $0)/../common/libbackend.sh
#
# You can use any valid BUILD_TYPE or BUILD_PROFILE, if you need to limit a backend to CUDA 12 only:
#
# LIMIT_TARGETS="cublas12"
# source $(dirname $0)/../common/libbackend.sh
#
# You can switch between uv (conda-like) and pip installation methods by setting USE_PIP:
# USE_PIP=true source $(dirname $0)/../common/libbackend.sh
#
# ===================== user-configurable defaults =====================
PYTHON_VERSION="${PYTHON_VERSION:-3.10}"      # e.g. 3.10 / 3.11 / 3.12 / 3.13
PYTHON_PATCH="${PYTHON_PATCH:-18}"            # e.g. 18 -> 3.10.18 ; 13 -> 3.11.13
PY_STANDALONE_TAG="${PY_STANDALONE_TAG:-20250818}"  # release tag date
# Enable/disable bundling of a portable Python build
PORTABLE_PYTHON="${PORTABLE_PYTHON:-false}"

# If you want to fully pin the filename (including tuned CPU targets), set:
# PORTABLE_PY_FILENAME="cpython-3.10.18+20250818-x86_64_v3-unknown-linux-gnu-install_only.tar.gz"
: "${PORTABLE_PY_FILENAME:=}"
: "${PORTABLE_PY_SHA256:=}"  # optional; if set we verify the download
# =====================================================================

# Default to uv if USE_PIP is not set
if [ "x${USE_PIP:-}" == "x" ]; then
    USE_PIP=false
fi

# ----------------------- helpers -----------------------
function _is_musl() {
    # detect musl (Alpine, etc)
    if command -v ldd >/dev/null 2>&1; then
        ldd --version 2>&1 | grep -qi musl && return 0
    fi
    # busybox-ish fallback
    if command -v getconf >/dev/null 2>&1; then
        getconf GNU_LIBC_VERSION >/dev/null 2>&1 || return 0
    fi
    return 1
}

function _triple() {
    local os="" arch="" libc="gnu"
    case "$(uname -s)" in
        Linux*)  os="unknown-linux" ;;
        Darwin*) os="apple-darwin" ;;
        MINGW*|MSYS*|CYGWIN*) os="pc-windows-msvc" ;;  # best-effort for Git Bash
        *) echo "Unsupported OS $(uname -s)"; exit 1;;
    esac

    case "$(uname -m)" in
        x86_64) arch="x86_64" ;;
        aarch64|arm64) arch="aarch64" ;;
        armv7l) arch="armv7" ;;
        i686|i386) arch="i686" ;;
        ppc64le) arch="ppc64le" ;;
        s390x) arch="s390x" ;;
        riscv64) arch="riscv64" ;;
        *) echo "Unsupported arch $(uname -m)"; exit 1;;
    esac

    if [[ "$os" == "unknown-linux" ]]; then
        if _is_musl; then
            libc="musl"
        else
            libc="gnu"
        fi
        echo "${arch}-${os}-${libc}"
    else
        echo "${arch}-${os}"
    fi
}

function _portable_dir() {
    echo "${EDIR}/python"
}

function _portable_bin() {
    # python-build-standalone puts python in ./bin
    echo "$(_portable_dir)/bin"
}

function _portable_python() {
    if [ -x "$(_portable_bin)/python3" ]; then
        echo "$(_portable_bin)/python3"
    else
        echo "$(_portable_bin)/python"
    fi
}


# macOS loader env for the portable CPython
_macosPortableEnv() {
  if [ "$(uname -s)" = "Darwin" ]; then
    export DYLD_LIBRARY_PATH="$(_portable_dir)/lib${DYLD_LIBRARY_PATH:+:${DYLD_LIBRARY_PATH}}"
    export DYLD_FALLBACK_LIBRARY_PATH="$(_portable_dir)/lib${DYLD_FALLBACK_LIBRARY_PATH:+:${DYLD_FALLBACK_LIBRARY_PATH}}"
  fi
}

# Good hygiene on macOS for downloaded/extracted trees
_unquarantinePortablePython() {
  if [ "$(uname -s)" = "Darwin" ]; then
    command -v xattr >/dev/null 2>&1 && xattr -dr com.apple.quarantine "$(_portable_dir)" || true
  fi
}

# ------------------ ### PORTABLE PYTHON ------------------
function ensurePortablePython() {
    local pdir="$(_portable_dir)"
    local pbin="$(_portable_bin)"
    local pyexe

    if [ -x "${pbin}/python3" ] || [ -x "${pbin}/python" ]; then
        _macosPortableEnv
        return 0
    fi

    mkdir -p "${pdir}"
    local triple="$(_triple)"

    local full_ver="${PYTHON_VERSION}.${PYTHON_PATCH}"
    local fn=""
    if [ -n "${PORTABLE_PY_FILENAME}" ]; then
        fn="${PORTABLE_PY_FILENAME}"
    else
        # generic asset name: cpython-<full_ver>+<tag>-<triple>-install_only.tar.gz
        fn="cpython-${full_ver}+${PY_STANDALONE_TAG}-${triple}-install_only.tar.gz"
    fi

    local url="https://github.com/astral-sh/python-build-standalone/releases/download/${PY_STANDALONE_TAG}/${fn}"
    local tmp="${pdir}/${fn}"
    echo "Downloading portable Python: ${fn}"
    # curl with retries; fall back to wget if needed
    if command -v curl >/dev/null 2>&1; then
        curl -L --fail --retry 3 --retry-delay 1 -o "${tmp}" "${url}"
    else
        wget -O "${tmp}" "${url}"
    fi

    if [ -n "${PORTABLE_PY_SHA256}" ]; then
        echo "${PORTABLE_PY_SHA256}  ${tmp}" | sha256sum -c -
    fi

    echo "Extracting ${fn} -> ${pdir}"
    # always a .tar.gz (we purposely choose install_only)
    tar -xzf "${tmp}" -C "${pdir}"
    rm -f "${tmp}"

    # Some archives nest a directory; if so, flatten to ${pdir}
    # Find the first dir with a 'bin/python*'
    local inner
    inner="$(find "${pdir}" -type f -path "*/bin/python*" -maxdepth 3 2>/dev/null | head -n1 || true)"
    if [ -n "${inner}" ]; then
        local inner_root
        inner_root="$(dirname "$(dirname "${inner}")")" # .../bin -> root
        if [ "${inner_root}" != "${pdir}" ]; then
            # move contents up one level
            shopt -s dotglob
            mv "${inner_root}/"* "${pdir}/"
            rm -rf "${inner_root}"
            shopt -u dotglob
        fi
    fi

    _unquarantinePortablePython
    _macosPortableEnv
    # Make sure it's runnable
    pyexe="$(_portable_python)"
    "${pyexe}" -V
}

# init handles the setup of the library
function init() {
    BACKEND_NAME=${PWD##*/}
    MY_DIR=$(realpath "$(dirname "$0")")
    BUILD_PROFILE=$(getBuildProfile)

    EDIR=${MY_DIR}
    if [ "x${ENV_DIR:-}" != "x" ]; then
        EDIR=${ENV_DIR}
    fi

    if [ ! -z "${LIMIT_TARGETS:-}" ]; then
        isValidTarget=$(checkTargets ${LIMIT_TARGETS})
        if [ ${isValidTarget} != true ]; then
            echo "${BACKEND_NAME} can only be used on the following targets: ${LIMIT_TARGETS}"
            exit 0
        fi
    fi

    echo "Initializing libbackend for ${BACKEND_NAME}"
}


# getBuildProfile will inspect the system to determine which build profile is appropriate:
# returns one of the following:
# - cublas11
# - cublas12
# - hipblas
# - intel
function getBuildProfile() {
    if [ x"${BUILD_TYPE:-}" == "xcublas" ] || [ x"${BUILD_TYPE:-}" == "xl4t" ]; then
        if [ ! -z "${CUDA_MAJOR_VERSION:-}" ]; then
            echo ${BUILD_TYPE}${CUDA_MAJOR_VERSION}
        else
            echo ${BUILD_TYPE}
        fi
        return 0
    fi

    if [ -d "/opt/intel" ]; then
        echo "intel"
        return 0
    fi

    if [ -n "${BUILD_TYPE:-}" ]; then
        echo ${BUILD_TYPE}
        return 0
    fi

    echo "cpu"
}


# Make the venv relocatable:
# - rewrite venv/bin/python{,3} to relative symlinks into $(_portable_dir)
# - normalize entrypoint shebangs to /usr/bin/env python3
# - optionally update pyvenv.cfg to point to the portable Python directory (only at runtime)
# Usage: _makeVenvPortable [--update-pyvenv-cfg]
_makeVenvPortable() {
    local update_pyvenv_cfg=false
    if [ "${1:-}" = "--update-pyvenv-cfg" ]; then
        update_pyvenv_cfg=true
    fi

    local venv_dir="${EDIR}/venv"
    local vbin="${venv_dir}/bin"

    [ -d "${vbin}" ] || return 0

    # 1) Replace python symlinks with relative ones to ../../python/bin/python3
    #    (venv/bin -> venv -> EDIR -> python/bin)
    local rel_py='../../python/bin/python3'

    for name in python3 python; do
        if [ -e "${vbin}/${name}" ] || [ -L "${vbin}/${name}" ]; then
            rm -f "${vbin}/${name}"
        fi
    done
    ln -s "${rel_py}" "${vbin}/python3"
    ln -s "python3" "${vbin}/python"

    # 2) Update pyvenv.cfg to point to the portable Python directory (only at runtime)
    #    Use absolute path resolved at runtime so it works when the venv is copied
    if [ "$update_pyvenv_cfg" = "true" ]; then
        local pyvenv_cfg="${venv_dir}/pyvenv.cfg"
        if [ -f "${pyvenv_cfg}" ]; then
            local portable_dir="$(_portable_dir)"
            # Resolve to absolute path - this ensures it works when the backend is copied
            # Only resolve if the directory exists (it should if ensurePortablePython was called)
            if [ -d "${portable_dir}" ]; then
                portable_dir="$(cd "${portable_dir}" && pwd)"
            else
                # Fallback to relative path if directory doesn't exist yet
                portable_dir="../python"
            fi
            local sed_i=(sed -i)
            # macOS/BSD sed needs a backup suffix; GNU sed doesn't. Make it portable:
            if sed --version >/dev/null 2>&1; then
                sed_i=(sed -i)
            else
                sed_i=(sed -i '')
            fi
            # Update the home field in pyvenv.cfg
            # Handle both absolute paths (starting with /) and relative paths
            if grep -q "^home = " "${pyvenv_cfg}"; then
                "${sed_i[@]}" "s|^home = .*|home = ${portable_dir}|" "${pyvenv_cfg}"
            else
                # If home field doesn't exist, add it
                echo "home = ${portable_dir}" >> "${pyvenv_cfg}"
            fi
        fi
    fi

    # 3) Rewrite shebangs of entry points to use env, so the venv is relocatable
    #    Only touch text files that start with #! and reference the current venv.
    local ve_abs="${vbin}/python"
    local sed_i=(sed -i)
    # macOS/BSD sed needs a backup suffix; GNU sed doesn't. Make it portable:
    if sed --version >/dev/null 2>&1; then
        sed_i=(sed -i)
    else
        sed_i=(sed -i '')
    fi

    for f in "${vbin}"/*; do
        [ -f "$f" ] || continue
        # Fast path: check first two bytes (#!)
        head -c2 "$f" 2>/dev/null | grep -q '^#!' || continue
        # Only rewrite if the shebang mentions the (absolute) venv python
        if head -n1 "$f" | grep -Fq "${ve_abs}"; then
            "${sed_i[@]}" '1s|^#!.*$|#!/usr/bin/env python3|' "$f"
            chmod +x "$f" 2>/dev/null || true
        fi
    done
}


# ensureVenv makes sure that the venv for the backend both exists, and is activated.
#
# This function is idempotent, so you can call it as many times as you want and it will
# always result in an activated virtual environment
function ensureVenv() {
    local interpreter=""

    if [ "x${PORTABLE_PYTHON}" == "xtrue" ] || [ -e "$(_portable_python)" ]; then
        echo "Using portable Python"
        ensurePortablePython
        interpreter="$(_portable_python)"
    else
        # Prefer system python${PYTHON_VERSION}, else python3, else fall back to bundled
        if command -v python${PYTHON_VERSION} >/dev/null 2>&1; then
            interpreter="python${PYTHON_VERSION}"
        elif command -v python3 >/dev/null 2>&1; then
            interpreter="python3"
        else
            echo "No suitable system Python found, bootstrapping portable build..."
            ensurePortablePython
            interpreter="$(_portable_python)"
        fi
    fi

    if [ ! -d "${EDIR}/venv" ]; then
        if [ "x${USE_PIP}" == "xtrue" ]; then
            "${interpreter}" -m venv --copies "${EDIR}/venv"
            source "${EDIR}/venv/bin/activate"
            "${interpreter}" -m pip install --upgrade pip
        else
            if [ "x${PORTABLE_PYTHON}" == "xtrue" ]; then
                uv venv --python "${interpreter}" "${EDIR}/venv"
            else
                uv venv --python "${PYTHON_VERSION}" "${EDIR}/venv"
            fi
        fi
        if [ "x${PORTABLE_PYTHON}" == "xtrue" ]; then
            # During install, only update symlinks and shebangs, not pyvenv.cfg
            _makeVenvPortable
        fi
    fi

    # We call it here to make sure that when we source a venv we can still use python as expected
    if [ -x "$(_portable_python)" ]; then
        _macosPortableEnv
    fi

    if [ "x${VIRTUAL_ENV:-}" != "x${EDIR}/venv" ]; then
        source "${EDIR}/venv/bin/activate"
    fi
}


function runProtogen() {
    ensureVenv
    if [ "x${USE_PIP}" == "xtrue" ]; then
        pip install grpcio-tools
    else
        uv pip install grpcio-tools
    fi
    pushd "${EDIR}" >/dev/null
        # use the venv python (ensures correct interpreter & sys.path)
        python -m grpc_tools.protoc -I../../ -I./ --python_out=. --grpc_python_out=. backend.proto
    popd >/dev/null
}


# installRequirements looks for several requirements files and if they exist runs the install for them in order
#
#  - requirements-install.txt
#  - requirements.txt
#  - requirements-${BUILD_TYPE}.txt
#  - requirements-${BUILD_PROFILE}.txt
#
# BUILD_PROFILE is a more specific version of BUILD_TYPE, ex: cuda-11 or cuda-12
# it can also include some options that we do not have BUILD_TYPES for, ex: intel
#
# NOTE: for BUILD_PROFILE==intel, this function does NOT automatically use the Intel python package index.
# you may want to add the following line to a requirements-intel.txt if you use one:
#
# --index-url https://pytorch-extension.intel.com/release-whl/stable/xpu/us/
#
# If you need to add extra flags into the pip install command you can do so by setting the variable EXTRA_PIP_INSTALL_FLAGS
# before calling installRequirements.  For example:
#
# source $(dirname $0)/../common/libbackend.sh
# EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"
# installRequirements
function installRequirements() {
    ensureVenv
    declare -a requirementFiles=(
        "${EDIR}/requirements-install.txt"
        "${EDIR}/requirements.txt"
        "${EDIR}/requirements-${BUILD_TYPE:-}.txt"
    )

    if [ "x${BUILD_TYPE:-}" != "x${BUILD_PROFILE}" ]; then
        requirementFiles+=("${EDIR}/requirements-${BUILD_PROFILE}.txt")
    fi
    if [ "x${BUILD_TYPE:-}" == "x" ]; then
        requirementFiles+=("${EDIR}/requirements-cpu.txt")
    fi
    requirementFiles+=("${EDIR}/requirements-after.txt")
    if [ "x${BUILD_TYPE:-}" != "x${BUILD_PROFILE}" ]; then
        requirementFiles+=("${EDIR}/requirements-${BUILD_PROFILE}-after.txt")
    fi

    # This is needed to build wheels that e.g. depends on Python.h
    if [ "x${PORTABLE_PYTHON}" == "xtrue" ]; then
        export C_INCLUDE_PATH="${C_INCLUDE_PATH:-}:$(_portable_dir)/include/python${PYTHON_VERSION}"
    fi

    for reqFile in ${requirementFiles[@]}; do
        if [ -f "${reqFile}" ]; then
            echo "starting requirements install for ${reqFile}"
            if [ "x${USE_PIP}" == "xtrue" ]; then
                pip install ${EXTRA_PIP_INSTALL_FLAGS:-} --requirement "${reqFile}"
            else
                uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} --requirement "${reqFile}"
            fi
            echo "finished requirements install for ${reqFile}"
        fi
    done

    runProtogen
}

# startBackend discovers and runs the backend GRPC server
#
# You can specify a specific backend file to execute by setting BACKEND_FILE before calling startBackend.
# example:
#
# source ../common/libbackend.sh
# BACKEND_FILE="${MY_DIR}/source/backend.py"
# startBackend $@
#
# valid filenames for autodiscovered backend servers are:
#  - server.py
#  - backend.py
#  - ${BACKEND_NAME}.py
function startBackend() {
    ensureVenv
    # Update pyvenv.cfg before running to ensure paths are correct for current location
    # This is critical when the backend position is dynamic (e.g., copied from container)
    if [ "x${PORTABLE_PYTHON}" == "xtrue" ] || [ -x "$(_portable_python)" ]; then
        _makeVenvPortable --update-pyvenv-cfg
    fi
    if [ ! -z "${BACKEND_FILE:-}" ]; then
        exec "${EDIR}/venv/bin/python" "${BACKEND_FILE}" "$@"
    elif [ -e "${MY_DIR}/server.py" ]; then
        exec "${EDIR}/venv/bin/python" "${MY_DIR}/server.py" "$@"
    elif [ -e "${MY_DIR}/backend.py" ]; then
        exec "${EDIR}/venv/bin/python" "${MY_DIR}/backend.py" "$@"
    elif [ -e "${MY_DIR}/${BACKEND_NAME}.py" ]; then
        exec "${EDIR}/venv/bin/python" "${MY_DIR}/${BACKEND_NAME}.py" "$@"
    fi
}


# runUnittests discovers and runs python unittests
#
# You can specify a specific test file to use by setting TEST_FILE before calling runUnittests.
# example:
#
# source ../common/libbackend.sh
# TEST_FILE="${MY_DIR}/source/test.py"
# runUnittests $@
#
# be default a file named test.py in the backends directory will be used
function runUnittests() {
    ensureVenv
    if [ ! -z "${TEST_FILE:-}" ]; then
        testDir=$(dirname "$(realpath "${TEST_FILE}")")
        testFile=$(basename "${TEST_FILE}")
        pushd "${testDir}" >/dev/null
        python -m unittest "${testFile}"
        popd >/dev/null
    elif [ -f "${MY_DIR}/test.py" ]; then
        pushd "${MY_DIR}" >/dev/null
        python -m unittest test.py
        popd >/dev/null
    else
        echo "no tests defined for ${BACKEND_NAME}"
    fi
}


##################################################################################
# Below here are helper functions not intended to be used outside of the library #
##################################################################################

# checkTargets determines if the current BUILD_TYPE or BUILD_PROFILE is in a list of valid targets
function checkTargets() {
    targets=$@
    declare -a targets=($targets)
    for target in ${targets[@]}; do
        if [ "x${BUILD_TYPE:-}" == "x${target}" ]; then
            echo true; return 0
        fi
        if [ "x${BUILD_PROFILE}" == "x${target}" ]; then
            echo true; return 0
        fi
    done
    echo false
}

init
