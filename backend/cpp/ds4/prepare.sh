#!/bin/bash
# Clones the upstream ds4 source at the pinned commit into ./ds4/. Idempotent.
set -e

DS4_REPO="${DS4_REPO:-https://github.com/antirez/ds4}"
DS4_VERSION="${DS4_VERSION:-ae302c2fa18cc6d9aefc021d0f27ae03c9ad2fc0}"

if [ -d ds4/.git ]; then
    current=$(git -C ds4 rev-parse HEAD 2>/dev/null || echo none)
    if [ "$current" = "$DS4_VERSION" ]; then
        echo "ds4 already at $DS4_VERSION"
        exit 0
    fi
    git -C ds4 fetch --depth 1 origin "$DS4_VERSION"
    git -C ds4 checkout "$DS4_VERSION"
    exit 0
fi

mkdir -p ds4
cd ds4
git init -q
git remote add origin "$DS4_REPO"
git fetch --depth 1 origin "$DS4_VERSION"
git checkout FETCH_HEAD
