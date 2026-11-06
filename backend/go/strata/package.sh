#!/bin/bash

# Copy the strata backend binary into the package dir for the final
# Dockerfile stage. The backend is pure Go and embeds its tokenizer helper
# script, so no extra files or runtime libraries are needed.

set -e

CURDIR=$(dirname "$(realpath $0)")

mkdir -p $CURDIR/package
cp -avf $CURDIR/strata $CURDIR/package/
cp -rfv $CURDIR/run.sh $CURDIR/package/
