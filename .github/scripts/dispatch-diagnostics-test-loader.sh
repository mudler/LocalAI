#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Test-only loader scope. Never install a driver stub or change an executable.
set -euo pipefail
stub=$(realpath -e "$1")
identity=$2
shift 2
[[ -f "$stub" ]]
readelf -d "$stub" | grep -E '\(SONAME\).*\[libcuda.so.1\]' > /dev/null
{
    sha256sum "$stub"
    readelf -h "$stub"
    readelf -d "$stub"
} > "$identity"
loader=$(mktemp -d)
trap 'rm -f "$loader/libcuda.so.1"; rmdir "$loader"' EXIT
ln -s "$stub" "$loader/libcuda.so.1"
LD_LIBRARY_PATH="$loader${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}" "$@"
