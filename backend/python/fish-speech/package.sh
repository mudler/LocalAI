#!/bin/bash

# Script to package runtime libraries for the fish-speech backend
# This is needed because the final Docker image is FROM scratch,
# so system libraries must be explicitly included.

set -e

CURDIR=$(dirname "$(realpath $0)")

# Create lib directory
mkdir -p $CURDIR/lib

echo "fish-speech packaging completed successfully"
ls -liah $CURDIR/lib/
