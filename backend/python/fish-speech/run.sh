#!/bin/bash
backend_dir=$(dirname "$(realpath "$0")")
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

export PYTHONPATH="${backend_dir}/fish-speech-src${PYTHONPATH:+:${PYTHONPATH}}"

startBackend $@
