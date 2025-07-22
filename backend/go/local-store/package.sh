#!/bin/bash

# Script to copy the appropriate libraries based on architecture
# This script is used in the final stage of the Dockerfile

set -e

CURDIR=$(dirname "$(realpath $0)")

mkdir -p $CURDIR/package
cp -avrf $CURDIR/local-store $CURDIR/package/
cp -rfv $CURDIR/run.sh $CURDIR/package/