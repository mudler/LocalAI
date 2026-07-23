#!/bin/bash

# Script to copy the appropriate libraries based on architecture
# This script is used in the final stage of the Dockerfile

set -e

CURDIR=$(dirname "$(realpath $0)")

# Create lib directory
mkdir -p $CURDIR/package/lib

cp -avf $CURDIR/silero-vad $CURDIR/package/
cp -avf $CURDIR/run.sh $CURDIR/package/
cp -rfLv $CURDIR/backend-assets/lib/* $CURDIR/package/lib/

# Detect architecture and copy appropriate libraries
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" "$CURDIR/package/silero-vad"

echo "Packaging completed successfully" 
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/