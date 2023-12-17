#!/bin/bash
set -xe
REPO=$1

LATEST_TAG=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | jq -r '.name')

cat <<< $(jq ".version = \"$LATEST_TAG\"" docs/data/version.json) > docs/data/version.json
