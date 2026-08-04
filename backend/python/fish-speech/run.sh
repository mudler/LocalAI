#!/bin/bash
backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# Editable installs record their build-time absolute source path, which becomes
# stale when the backend is relocated under /backends at install time.
export PYTHONPATH="${EDIR}/fish-speech-src${PYTHONPATH:+:${PYTHONPATH}}"

startBackend "$@"
