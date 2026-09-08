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

# .github/vllm-release-tag.commit form: a lone vLLM release tag.
assert_version "0.28.0" 'v0.28.0'
assert_version "0.28.0" '0.28.0'
assert_version "0.28.0" '  v0.28.0  '

# A whole upstream installer must still yield the inline pin, not a version-like
# fragment of some other line.
assert_version "0.26.0" 'set -e
vllm_wheel="vllm-1.2.3-cp312.whl"
VLLM_VERSION="0.26.0"'

if printf '%s\n' 'VLLM_VERSION="not-a-version"' | "$extractor"; then
    echo "malformed versions must be rejected" >&2
    exit 1
fi

if printf '%s\n' 'VLLM_VERSION="0.26.0"garbage' | "$extractor"; then
    echo "trailing assignment content must be rejected" >&2
    exit 1
fi

if printf '%s\n' 'not-a-tag' | "$extractor"; then
    echo "malformed release tags must be rejected" >&2
    exit 1
fi

if printf '%s\n' 'vllm-1.2.3-cp312.whl' | "$extractor"; then
    echo "a version embedded in a longer line must be rejected" >&2
    exit 1
fi
