#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# This is here because the Intel pip index is broken and returns 200 status codes for every package name, it just doesn't return any package links.
# This makes uv think that the package exists in the Intel pip index, and by default it stops looking at other pip indexes once it finds a match.
# We need uv to continue falling through to the pypi default index to find optimum[openvino] in the pypi index
# the --upgrade actually allows us to *downgrade* torch to the version provided in the Intel pip index
if [ "x${BUILD_PROFILE}" == "xintel" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
fi

# This is here because the jetson-ai-lab.io PyPI mirror's root PyPI endpoint (pypi.jetson-ai-lab.io/root/pypi/)
# returns 503 errors when uv tries to fall back to it for packages not found in the specific subdirectory.
# We need uv to continue falling through to the official PyPI index when it encounters these errors.
if [ "x${BUILD_PROFILE}" == "xl4t12" ] || [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-first-match"
fi

EXTRA_PIP_INSTALL_FLAGS+=" --no-build-isolation"

installRequirements
