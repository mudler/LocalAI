#!/bin/bash
set -e

# liquid-audio requires Python ≥ 3.12 (per its pyproject.toml); the default
# portable Python in libbackend.sh is 3.10. Override before sourcing.
export PYTHON_VERSION="${PYTHON_VERSION:-3.12}"
export PYTHON_PATCH="${PYTHON_PATCH:-11}"

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# liquid-audio's torch wheels are large; allow upgrades to satisfy transitive pins
EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
installRequirements
