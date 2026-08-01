#!/bin/bash
set -e

backend_dir=$(dirname $0)

for cuda_version in 12 13; do
    grep -qx "flash-attn" "$backend_dir/requirements-cublas${cuda_version}-after.txt"
done

grep -q 'BUILD_PROFILE.*cublas13' "$backend_dir/install.sh"
grep -q 'MAX_JOBS.*1' "$backend_dir/install.sh"
grep -q 'NVCC_THREADS.*1' "$backend_dir/install.sh"

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

runUnittests
