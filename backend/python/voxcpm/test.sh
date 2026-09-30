#!/bin/bash
set -e

backend_dir=$(dirname $0)
bash "$backend_dir/install_test.sh"
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

runUnittests
