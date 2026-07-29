#!/bin/bash
set -e

EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

if [ "x${BUILD_PROFILE}" == "xintel" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
fi

# Darwin needs a newer interpreter than libbackend's 3.10 default. nemo_toolkit
# pulls in text2num, a Rust extension built with maturin, and its macOS arm64
# wheels start at cp311 (3.0.2 publishes cp311/cp312/cp313/cp314 and no cp310).
# On 3.10 pip therefore falls back to the sdist and dies in the PEP 517 hook
# with "No module named 'maturin'", since EXTRA_PIP_INSTALL_FLAGS carries
# --no-build-isolation and nothing installs the build backend. Moving to 3.12
# takes the prebuilt wheel and needs no Rust toolchain on the runner at all.
#
# Darwin only, deliberately: the Linux profiles resolve a cp310 manylinux wheel
# for the same package and have no reason to move.
if [ "x${BUILD_PROFILE}" == "xmps" ] || [ "x${BUILD_PROFILE}" == "xmetal" ]; then
    PYTHON_VERSION="3.12"
    # PYTHON_PATCH must move with it. libbackend builds the portable-Python URL
    # as cpython-${PYTHON_VERSION}.${PYTHON_PATCH}+${PY_STANDALONE_TAG}-..., and
    # the default patch is 18 for 3.10.18; leaving it alone asks for a 3.12.18
    # that was never released and the download 404s.
    #
    # 11, not the 12 that sglang/install.sh uses for l4t13: at the 20250818 tag
    # python-build-standalone published 3.12.12 for linux aarch64 but not for
    # aarch64-apple-darwin, where 3.12.11 is the newest. Verified against the
    # release assets rather than copied across.
    PYTHON_PATCH="11"
fi

installRequirements
