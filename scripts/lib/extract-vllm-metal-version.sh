#!/bin/bash
set -euo pipefail

# Print the bare X.Y.Z vLLM version a vllm-metal release builds against, reading
# whichever form the release declares it in on stdin:
#
#   * .github/vllm-release-tag.commit -- a lone "vX.Y.Z" vLLM release tag. This is
#     the source of truth since vllm-metal 0.28, which also re-versioned the
#     project so its own version tracks the vLLM version it targets.
#   * install.sh -- releases predating that file pinned VLLM_VERSION="X.Y.Z"
#     (earlier still: vllm_v="X.Y.Z") inline in their installer.
grep -m1 -oE '^[[:space:]]*((local[[:space:]]+)?(vllm_v|VLLM_VERSION)[[:space:]]*=[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+"[[:space:]]*(#.*)?|v?[0-9]+\.[0-9]+\.[0-9]+[[:space:]]*)$' \
    | grep -oE '[0-9]+\.[0-9]+\.[0-9]+'
