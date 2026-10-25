#!/bin/sh
# SPDX-License-Identifier: MIT
# Usage: sh tests/result_api.sh [path-to-stable-diffusion.cpp]
set -eu
backend=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
upstream=${1:-"$backend/sources/stablediffusion-ggml.cpp"}
work=$(mktemp -d "${TMPDIR:-/tmp}/gosd-result-api.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
case $(uname -s) in
    Darwin) gc=-Wl,-dead_strip ;;
    *) gc=-Wl,--gc-sections ;;
esac
# CXXFLAGS/LDFLAGS may enable sanitizers; do not use -DNDEBUG (assertions test behavior).
${CXX:-c++} ${CXXFLAGS:-} -std=c++17 -ffunction-sections -fdata-sections \
    -I"$upstream/include" -I"$upstream/thirdparty" \
    "$backend/tests/result_api.cpp" "$gc" ${LDFLAGS:-} -o "$work/result-api"
"$work/result-api" "$work"
