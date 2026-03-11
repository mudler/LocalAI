#!/bin/bash

# Script to package runtime libraries for the fish-speech backend
# This is needed because the final Docker image is FROM scratch,
# so system libraries must be explicitly included.

set -e

CURDIR=$(dirname "$(realpath $0)")

# Create lib directory
mkdir -p $CURDIR/lib

# Package portaudio shared library (required by pyaudio, a transitive dep of fish-speech)
for lib_path in /usr/lib/x86_64-linux-gnu /usr/lib/aarch64-linux-gnu; do
    if [ -d "$lib_path" ]; then
        for lib in "$lib_path"/libportaudio.so*; do
            if [ -e "$lib" ]; then
                cp -avfL "$lib" "$CURDIR/lib/"
            fi
        done
    fi
done

echo "fish-speech packaging completed successfully"
ls -liah $CURDIR/lib/
