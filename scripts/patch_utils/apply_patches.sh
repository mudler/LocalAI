#!/bin/bash
# apply_patches.sh — Generic patch fetcher and applier for any backend.
#
# Usage: ./apply_patches.sh <source-dir> <target-dir>
#
#   <source-dir>  Directory containing a patches/ folder (with optional sources.yaml)
#   <target-dir>  The cloned upstream repo to patch (e.g., llama.cpp/)
#
# Behavior (idempotent):
#   1. If patches/sources.yaml exists and yq is available, for each source:
#      - If patches/<name>/ already has .patch files: skip fetching (vendored)
#      - Otherwise: clone the fork, auto-detect the fork base via merge-base
#        with the upstream repo, and generate patches
#   2. Apply all patches from source subdirectories (alphabetical), then top-level .patch files
#   3. Fails fast on any patch application error

set -e

apply_patches() {
    local SOURCE_DIR="$(cd "$1" && pwd)"
    local TARGET_DIR="$2"
    local PATCHES_DIR="$SOURCE_DIR/patches"

    if [ ! -d "$PATCHES_DIR" ]; then
        return 0
    fi

    # Phase 1: Generate missing patches from fork sources
    if [ -f "$PATCHES_DIR/sources.yaml" ] && command -v yq &>/dev/null; then
        local SOURCE_COUNT
        SOURCE_COUNT=$(yq '.sources | length' "$PATCHES_DIR/sources.yaml")

        for i in $(seq 0 $((SOURCE_COUNT - 1))); do
            local NAME REPO BRANCH UPSTREAM_REPO
            NAME=$(yq ".sources[$i].name" "$PATCHES_DIR/sources.yaml")
            REPO=$(yq ".sources[$i].repo" "$PATCHES_DIR/sources.yaml")
            BRANCH=$(yq ".sources[$i].branch" "$PATCHES_DIR/sources.yaml")
            UPSTREAM_REPO=$(yq ".sources[$i].upstream_repo" "$PATCHES_DIR/sources.yaml")

            local SOURCE_PATCH_DIR="$PATCHES_DIR/$NAME"
            local EXISTING
            EXISTING=$(ls "$SOURCE_PATCH_DIR"/*.patch 2>/dev/null | wc -l)

            if [ "$EXISTING" -gt 0 ]; then
                echo "Patches [$NAME]: $EXISTING patches already present — skipping fetch."
            else
                echo "Patches [$NAME]: fetching from $REPO ($BRANCH)"

                local TMPDIR
                TMPDIR=$(mktemp -d)

                if git clone --single-branch -b "$BRANCH" "$REPO" "$TMPDIR/fork" 2>&1; then
                    cd "$TMPDIR/fork"

                    # Auto-detect fork base: merge-base between fork and upstream
                    git remote add upstream "$UPSTREAM_REPO"
                    git fetch upstream 2>&1

                    local FORK_BASE
                    FORK_BASE=$(git merge-base HEAD upstream/master 2>/dev/null || \
                                git merge-base HEAD upstream/main 2>/dev/null || echo "")

                    if [ -z "$FORK_BASE" ]; then
                        echo "WARNING: Could not find merge-base with upstream — skipping source '$NAME'"
                        cd "$SOURCE_DIR"
                        rm -rf "$TMPDIR"
                        continue
                    fi

                    local PATCH_COUNT
                    PATCH_COUNT=$(git rev-list --count "$FORK_BASE"..HEAD 2>/dev/null || echo "0")
                    echo "  Fork base: ${FORK_BASE:0:12} ($PATCH_COUNT commits to extract)"

                    if [ "$PATCH_COUNT" -gt 0 ]; then
                        mkdir -p "$SOURCE_PATCH_DIR"
                        git format-patch "$FORK_BASE"..HEAD -o "$SOURCE_PATCH_DIR/" >/dev/null 2>&1
                        echo "  Generated $PATCH_COUNT patches in patches/$NAME/"
                    fi
                    cd "$SOURCE_DIR"
                else
                    echo "WARNING: Failed to clone $REPO — skipping source '$NAME'"
                fi

                rm -rf "$TMPDIR"
            fi
        done
    elif [ -f "$PATCHES_DIR/sources.yaml" ]; then
        echo "WARNING: yq not found — skipping source-based patch generation."
    fi

    # Phase 2: Apply patches (subdirectories first, then top-level)
    for source_dir in $(find "$PATCHES_DIR" -mindepth 1 -maxdepth 1 -type d | sort); do
        for p in $(ls "$source_dir"/*.patch 2>/dev/null | sort); do
            echo "Applying: $(basename "$source_dir")/$(basename "$p")"
            patch -d "$TARGET_DIR" -p1 < "$p" || { echo "FAILED: $p"; exit 1; }
        done
    done
    for p in $(ls "$PATCHES_DIR"/*.patch 2>/dev/null | sort); do
        echo "Applying: $(basename "$p")"
        patch -d "$TARGET_DIR" -p1 < "$p" || { echo "FAILED: $p"; exit 1; }
    done
}

# Run with arguments
if [ $# -lt 2 ]; then
    echo "Usage: $0 <source-dir> <target-dir>"
    exit 1
fi
apply_patches "$1" "$2"
