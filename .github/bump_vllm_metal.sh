#!/bin/bash
# Bump the vllm-metal pins in the vLLM backend's darwin (Apple Silicon) install
# path. The macOS/Metal build (backend/python/vllm/install.sh, Darwin branch)
# installs vllm-metal, which is version-locked to a specific vLLM source release.
# Two values must move together:
#   VLLM_METAL_VERSION -> the vllm-metal GitHub release tag (its prebuilt wheel)
#   VLLM_VERSION       -> the vLLM source version that release builds against
# vllm-metal declares the latter in its OWN install.sh as `vllm_v="X.Y.Z"`. This
# script reads both from vllm-metal's latest release and rewrites them atomically
# -- mirroring bump_vllm_wheel.sh, which does the same for the Linux cu130 wheel.
#
# This deliberately tracks vllm-project/vllm-metal, NOT vllm-project/vllm: the
# darwin build can only use the exact vLLM version vllm-metal supports, so it may
# lag the Linux pin (requirements-cublas13-after.txt) until vllm-metal catches up.
set -xe
REPO=$1   # vllm-project/vllm-metal
FILE=$2   # backend/python/vllm/install.sh
VAR=$3    # VLLM_METAL_VERSION (used for the workflow's output file names)

if [ -z "$FILE" ] || [ -z "$REPO" ] || [ -z "$VAR" ]; then
    echo "usage: $0 <repo> <install-file> <var-name>" >&2
    exit 1
fi

# vllm-metal ships frequent dev releases, all flagged as non-prerelease, so
# /releases/latest returns the newest one (with its cp312 wheel asset).
LATEST_TAG=$(curl -sS -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/$REPO/releases/latest" \
    | python3 -c "import json,sys; print(json.load(sys.stdin)['tag_name'])")

# The coupled vLLM source version lives in vllm-metal's installer at that tag.
NEW_VLLM_VERSION=$(curl -fsSL \
    "https://raw.githubusercontent.com/$REPO/$LATEST_TAG/install.sh" \
    | grep -oE 'vllm_v="[0-9]+\.[0-9]+\.[0-9]+"' | head -1 | cut -d'"' -f2)

if [ -z "$LATEST_TAG" ] || [ -z "$NEW_VLLM_VERSION" ]; then
    echo "Could not resolve vllm-metal tag ($LATEST_TAG) or its vllm_v ($NEW_VLLM_VERSION)." >&2
    exit 1
fi

set +e
CURRENT_TAG=$(grep -oE 'VLLM_METAL_VERSION="[^"]*"' "$FILE" | head -1 | cut -d'"' -f2)
set -e

# Rewrite both pins. peter-evans/create-pull-request opens no PR on a clean tree,
# so a no-op rewrite (already current) is safe.
sed -i "$FILE" \
    -e "s|VLLM_METAL_VERSION=\"[^\"]*\"|VLLM_METAL_VERSION=\"$LATEST_TAG\"|" \
    -e "s|VLLM_VERSION=\"[^\"]*\"|VLLM_VERSION=\"$NEW_VLLM_VERSION\"|"

if [ -z "$CURRENT_TAG" ]; then
    echo "Could not find VLLM_METAL_VERSION=\"...\" in $FILE." >&2
    exit 0
fi

echo "vllm-metal ${CURRENT_TAG} -> ${LATEST_TAG} (builds vLLM ${NEW_VLLM_VERSION}): https://github.com/$REPO/releases/tag/${LATEST_TAG}" >> "${VAR}_message.txt"
echo "${LATEST_TAG}" >> "${VAR}_commit.txt"
