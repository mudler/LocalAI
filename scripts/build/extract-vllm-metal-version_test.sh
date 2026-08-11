#!/bin/bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
extractor="$repo_root/scripts/lib/extract-vllm-metal-version.sh"

assert_version() {
    local expected=$1
    local input=$2
    local actual

    actual=$(printf '%s\n' "$input" | "$extractor")
    if [ "$actual" != "$expected" ]; then
        echo "expected version $expected, got $actual" >&2
        exit 1
    fi
}

assert_version "0.23.0" 'vllm_v="0.23.0"'
assert_version "0.26.0" '  local vllm_v="0.26.0"'
assert_version "0.26.0" 'VLLM_VERSION="0.26.0"'
assert_version "0.26.1" '  VLLM_VERSION = "0.26.1" # comment'

if printf '%s\n' 'VLLM_VERSION="not-a-version"' | "$extractor"; then
    echo "malformed versions must be rejected" >&2
    exit 1
fi

if printf '%s\n' 'VLLM_VERSION="0.26.0"garbage' | "$extractor"; then
    echo "trailing assignment content must be rejected" >&2
    exit 1
fi
