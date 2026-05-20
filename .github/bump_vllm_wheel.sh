#!/bin/bash
# Bump the cublas13 vLLM wheel pin in requirements-cublas13-after.txt.
#
# vLLM's PyPI wheel is built against CUDA 12 so the cublas13 build pulls a
# cu130-flavoured wheel from vLLM's per-tag index at
# https://wheels.vllm.ai/<TAG>/cu130/. That URL segment is itself version-locked
# (no /latest/ alias upstream), so bumping vLLM means rewriting both the URL
# segment and the version constraint atomically. bump_deps.sh handles git-sha
# vars in Makefiles; this script handles the two-value rewrite specific to the
# vLLM requirements file.
set -xe
REPO=$1   # vllm-project/vllm
FILE=$2   # backend/python/vllm/requirements-cublas13-after.txt
VAR=$3    # VLLM_VERSION (used for output file names so the workflow can read them)

if [ -z "$FILE" ] || [ -z "$REPO" ] || [ -z "$VAR" ]; then
    echo "usage: $0 <repo> <requirements-file> <var-name>" >&2
    exit 1
fi

# /releases/latest returns the most recent non-prerelease tag.
LATEST_TAG=$(curl -sS -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/$REPO/releases/latest" \
    | python3 -c "import json,sys; print(json.load(sys.stdin)['tag_name'])")

# Strip leading 'v' (vLLM tags are 'v0.20.0', the URL/version use '0.20.0').
NEW_VERSION="${LATEST_TAG#v}"

set +e
CURRENT_VERSION=$(grep -oE '^vllm==[0-9]+\.[0-9]+\.[0-9]+' "$FILE" | head -1 | cut -d= -f3)
set -e

# sed both lines unconditionally — peter-evans/create-pull-request opens no PR
# when the working tree is clean, so a no-op rewrite is safe.
sed -i "$FILE" \
    -e "s|wheels\.vllm\.ai/[^/]*/cu130|wheels.vllm.ai/$NEW_VERSION/cu130|g" \
    -e "s|^vllm==.*|vllm==$NEW_VERSION|"

if [ -z "$CURRENT_VERSION" ]; then
    echo "Could not find vllm==X.Y.Z in $FILE."
    exit 0
fi

echo "Changes: https://github.com/$REPO/compare/v${CURRENT_VERSION}...${LATEST_TAG}" >> "${VAR}_message.txt"
echo "${NEW_VERSION}" >> "${VAR}_commit.txt"
