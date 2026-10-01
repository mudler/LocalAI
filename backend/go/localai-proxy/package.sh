#!/bin/bash

# Script to copy the localai-proxy binary into the package dir for the
# final Dockerfile stage. Mirrors backend/go/local-store/package.sh —
# no extra runtime libs needed since the backend is pure Go.

set -e

CURDIR=$(dirname "$(realpath $0)")

mkdir -p $CURDIR/package
cp -avf $CURDIR/localai-proxy $CURDIR/package/
cp -rfv $CURDIR/run.sh $CURDIR/package/
