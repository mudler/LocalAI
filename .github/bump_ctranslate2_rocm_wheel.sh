#!/bin/bash
# Bump the CTranslate2 ROCm Python wheel release pin used by faster-whisper.
set -xe

REPO=$1   # OpenNMT/CTranslate2
FILE=$2   # backend/python/faster-whisper/install.sh
VAR=$3    # CTRANSLATE2_VERSION (used for output file names so the workflow can read them)

if [ -z "$FILE" ] || [ -z "$REPO" ] || [ -z "$VAR" ]; then
    echo "usage: $0 <repo> <install-script> <var-name>" >&2
    exit 1
fi

LATEST_RELEASE=$(curl -sS -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/$REPO/releases/latest")
LATEST_TAG=$(python3 -c "import json,sys; print(json.load(sys.stdin)['tag_name'])" <<< "$LATEST_RELEASE")

set +e
CURRENT_VERSION=$(grep -m1 "^${VAR}=" "$FILE" | cut -d= -f2 | sed -E 's/^\$\{[^:]+:-([^}]+)\}$/\1/')
CTRANSLATE2_ROCM_WHEEL_OS=$(grep -m1 '^CTRANSLATE2_ROCM_WHEEL_OS=' "$FILE" | cut -d= -f2 | sed -E 's/^\$\{[^:]+:-([^}]+)\}$/\1/')
set -e

if [ -z "$CURRENT_VERSION" ]; then
    echo "Could not find $VAR in $FILE."
    exit 0
fi

if [ -z "$CTRANSLATE2_ROCM_WHEEL_OS" ]; then
    echo "Could not find CTRANSLATE2_ROCM_WHEEL_OS in $FILE."
    exit 0
fi

ASSET_NAME="rocm-python-wheels-${CTRANSLATE2_ROCM_WHEEL_OS}.zip"
LATEST_RELEASE="$LATEST_RELEASE" python3 - "$ASSET_NAME" <<'PY'
import json
import os
import sys

asset_name = sys.argv[1]
release = json.loads(os.environ["LATEST_RELEASE"])
assets = {asset.get("name") for asset in release.get("assets", [])}
if asset_name not in assets:
    raise SystemExit(f"Could not find release asset {asset_name!r}")
PY

sed -i "$FILE" -e "s/^${VAR}=.*/${VAR}=\${${VAR}:-${LATEST_TAG}}/"

echo "Changes: https://github.com/$REPO/compare/${CURRENT_VERSION}...${LATEST_TAG}" >> "${VAR}_message.txt"
echo "${LATEST_TAG}" >> "${VAR}_commit.txt"
