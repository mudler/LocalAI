#!/bin/bash
set -xe

source "$(dirname "${BASH_SOURCE[0]}")/gh_curl.sh"

REPO=$1

LATEST_TAG=$(gh_curl -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/$REPO/releases/latest" | jq -r '.tag_name')

# jq prints the string "null" for a missing key, so a throttled or otherwise
# unexpected API response would otherwise be published as the docs version.
if [ -z "$LATEST_TAG" ] || [ "$LATEST_TAG" = "null" ]; then
    echo "Refusing to bump docs version: could not resolve the latest release tag for $REPO." >&2
    exit 1
fi

cat <<< $(jq ".version = \"$LATEST_TAG\"" docs/data/version.json) > docs/data/version.json
