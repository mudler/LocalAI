#!/bin/bash
set -e

backend_dir=$(dirname $0)

for cuda_version in 12 13; do
    grep -qx "flash-attn" "$backend_dir/requirements-cublas${cuda_version}-after.txt"
done

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

runUnittests
