#!/bin/bash
# Bump the Strata pins in backend/go/strata/Makefile to the latest release.
#
# The strata backend does not build the engine. It fetches the engine packages
# upstream publishes with each release, so a bump has to move several values
# together: the release tag, the commit of that tag (the Python modules come from
# it), the engine package version, and the SHA-256 of every package. bump_deps.sh
# only handles one commit variable, hence this script.
#
# Each package's SHA-256 comes from the release's SHA256SUMS.txt and must equal
# the digest GitHub computed for the uploaded asset. The pull request this opens
# is the review step; nothing is trusted after that except those pins.
set -xe

source "$(dirname "${BASH_SOURCE[0]}")/gh_curl.sh"

REPO=$1   # Niko1221/Strata
FILE=$2   # backend/go/strata/Makefile
VAR=$3    # STRATA_VERSION (used for the output file names the workflow reads)

if [ -z "$REPO" ] || [ -z "$FILE" ] || [ -z "$VAR" ]; then
    echo "usage: $0 <repo> <makefile> <var-name>" >&2
    exit 1
fi

RELEASE=$(gh_curl -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/$REPO/releases/latest")
TAG=$(echo "$RELEASE" | python3 -c "import json,sys; print(json.load(sys.stdin)['tag_name'])")

# The commit the tag points at (the API resolves annotated tags).
COMMIT=$(gh_curl -H "Accept: application/vnd.github.VERSION.sha" \
    "https://api.github.com/repos/$REPO/commits/$TAG" || true)
if ! [[ "$COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
    echo "Refusing to bump: expected a 40-char commit SHA for $REPO@$TAG, got: $COMMIT" >&2
    exit 1
fi

SUMS=$(gh_curl -L "https://github.com/$REPO/releases/download/$TAG/SHA256SUMS.txt")

set +e
CURRENT_TAG="$(grep -m1 "^STRATA_VERSION?=" "$FILE" | cut -d'=' -f2)"
set -e

# Work out the new engine version and the new sums into a temporary list first,
# so a half-finished run never leaves a half-rewritten Makefile.
NEW_ENGINE_VERSION=""
PINS=$(mktemp)
for var in $(grep -oE '^STRATA_SHA256_[a-z0-9_]+' "$FILE"); do
    key="${var#STRATA_SHA256_}"
    # linux_x86_64_cuda_cuda12 -> linux-x86_64-cuda-cuda12
    suffix=$(echo "$key" | sed -e 's/x86_64/X8664/' -e 's/_/-/g' -e 's/X8664/x86_64/')
    line=$(echo "$SUMS" | grep -E "  strata-engine-[0-9.]+-${suffix}\.tar\.gz\$" || true)
    if [ "$(echo "$line" | grep -c .)" != "1" ]; then
        echo "Refusing to bump: $TAG has no single engine package for $suffix" >&2
        exit 1
    fi
    sum=$(echo "$line" | cut -d' ' -f1)
    name=$(echo "$line" | awk '{print $2}')
    ver=$(echo "$name" | sed -E 's/^strata-engine-([0-9.]+)-.*/\1/')
    if [ -n "$NEW_ENGINE_VERSION" ] && [ "$NEW_ENGINE_VERSION" != "$ver" ]; then
        echo "Refusing to bump: engine packages of $TAG disagree on the version ($NEW_ENGINE_VERSION, $ver)" >&2
        exit 1
    fi
    NEW_ENGINE_VERSION="$ver"
    # GitHub's own digest for the uploaded asset must agree with SHA256SUMS.txt.
    digest=$(echo "$RELEASE" | python3 -c "
import json, sys
name = sys.argv[1]
for a in json.load(sys.stdin)['assets']:
    if a['name'] == name:
        print((a.get('digest') or '').removeprefix('sha256:'))
" "$name")
    if [ "$digest" != "$sum" ]; then
        echo "Refusing to bump: SHA256SUMS.txt and the GitHub asset digest disagree for $name ($sum vs '$digest')" >&2
        exit 1
    fi
    echo "$var $sum" >> "$PINS"
done

# Every value is checked; now rewrite them all. A no-op rewrite is safe:
# peter-evans/create-pull-request opens no PR when the tree is clean.
sed -i "$FILE" \
    -e "s/^STRATA_VERSION?=.*/STRATA_VERSION?=$TAG/" \
    -e "s/^STRATA_COMMIT?=.*/STRATA_COMMIT?=$COMMIT/" \
    -e "s/^STRATA_ENGINE_VERSION?=.*/STRATA_ENGINE_VERSION?=$NEW_ENGINE_VERSION/"
while read -r var sum; do
    sed -i "$FILE" -e "s/^$var?=.*/$var?=$sum/"
done < "$PINS"
rm -f "$PINS"

if [ -z "$CURRENT_TAG" ]; then
    echo "Could not find STRATA_VERSION in $FILE."
    exit 0
fi

echo "Changes: https://github.com/$REPO/compare/${CURRENT_TAG}...${TAG}" >> "${VAR}_message.txt"
echo "${TAG} (${COMMIT})" >> "${VAR}_commit.txt"
