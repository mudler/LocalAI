#!/bin/bash
set -e

source $(dirname $0)/../common/libbackend.sh

# Download checkpoints if not present
if [ ! -d "checkpoints_v2" ]; then
    wget https://myshell-public-repo-host.s3.amazonaws.com/openvoice/checkpoints_v2_0417.zip -O checkpoints_v2.zip
    unzip checkpoints_v2.zip
fi

runUnittests
