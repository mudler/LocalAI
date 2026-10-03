#!/bin/bash

set -e

CURDIR=$(dirname "$(realpath "$0")")

mkdir -p "$CURDIR/package"
cp -avf "$CURDIR/qdrant-store" "$CURDIR/package/"
cp -rfv "$CURDIR/run.sh" "$CURDIR/package/"
