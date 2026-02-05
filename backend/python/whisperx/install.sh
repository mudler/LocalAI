#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

if [ "x${BUILD_PROFILE}" != "xmetal" ] && [ "x${BUILD_PROFILE}" != "xmps" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy unsafe-best-match"
fi

installRequirements
