#!/bin/bash

# Script to copy the appropriate libraries based on architecture
# This script is used in the final stage of the Dockerfile

set -e

CURDIR=$(dirname "$(realpath $0)")

# Create lib directory
mkdir -p $CURDIR/package/lib

cp -avf $CURDIR/piper $CURDIR/package/
cp -avf $CURDIR/espeak-ng-data $CURDIR/package/
cp -rfv $CURDIR/run.sh $CURDIR/package/
cp -rfLv $CURDIR/sources/go-piper/piper-phonemize/pi/lib/* $CURDIR/package/lib/

# Detect architecture and copy appropriate libraries
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" "$CURDIR/package/piper"

echo "Packaging completed successfully" 
ls -liah $CURDIR/package/
ls -liah $CURDIR/package/lib/