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
#      - Otherwise: clone the fork at a pinned SHA, diff against the pinned
#        upstream SHA, and generate patches
#   2. Apply all patches (skips already-applied ones)
#   3. Fails fast on any patch application error
#
# sources.yaml fields:
#   name         — subdirectory name for this source's patches
#   repo         — fork git URL
#   version_var  — Makefile variable holding the pinned fork commit SHA
#   base_var     — Makefile variable holding the pinned upstream commit SHA
#   version_file — Makefile path (relative to backend dir)

set -e

# Use /tmp for patch temp files to avoid macOS long-path issues
export TMPDIR="${TMPDIR_OVERRIDE:-/tmp}"

read_makefile_var() {
    grep -m1 "^${1}?=" "$2" | cut -d'=' -f2
}

apply_one_patch() {
    local target_dir="$1"
    local patch_file="$2"
    local label="$3"

    if patch -d "$target_dir" -p1 --reverse --dry-run < "$patch_file" >/dev/null 2>&1; then
        echo "  Already applied, skipping: $label"
        return 0
    fi

    echo "  Applying: $label"
    patch -d "$target_dir" -p1 --forward < "$patch_file" || { echo "FAILED: $patch_file"; exit 1; }
}

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
            local NAME REPO VERSION_VAR BASE_VAR VERSION_FILE
            NAME=$(yq ".sources[$i].name" "$PATCHES_DIR/sources.yaml")
            REPO=$(yq ".sources[$i].repo" "$PATCHES_DIR/sources.yaml")
            VERSION_VAR=$(yq ".sources[$i].version_var" "$PATCHES_DIR/sources.yaml")
            BASE_VAR=$(yq ".sources[$i].base_var" "$PATCHES_DIR/sources.yaml")
            VERSION_FILE=$(yq ".sources[$i].version_file" "$PATCHES_DIR/sources.yaml")

            local MAKEFILE="$SOURCE_DIR/$VERSION_FILE"
            local FORK_SHA BASE_SHA
            FORK_SHA=$(read_makefile_var "$VERSION_VAR" "$MAKEFILE")
            BASE_SHA=$(read_makefile_var "$BASE_VAR" "$MAKEFILE")

            if [ -z "$FORK_SHA" ] || [ -z "$BASE_SHA" ]; then
                echo "WARNING: Could not read $VERSION_VAR or $BASE_VAR from $MAKEFILE — skipping '$NAME'"
                continue
            fi

            local SOURCE_PATCH_DIR="$PATCHES_DIR/$NAME"
            local EXISTING
            EXISTING=$(ls "$SOURCE_PATCH_DIR"/*.patch 2>/dev/null | wc -l)

            if [ "$EXISTING" -gt 0 ]; then
                echo "Patches [$NAME]: $EXISTING patches already present — skipping fetch."
            else
                echo "Patches [$NAME]: generating from $REPO"
                echo "  base (upstream): ${BASE_SHA:0:12}"
                echo "  head (fork):     ${FORK_SHA:0:12}"

                local TMPDIR_CLONE
                TMPDIR_CLONE=$(mktemp -d)

                if git clone "$REPO" "$TMPDIR_CLONE/fork" 2>&1; then
                    cd "$TMPDIR_CLONE/fork"

                    # Fetch the upstream base commit (may not be in the fork's history)
                    git fetch origin "$FORK_SHA" 2>&1 || true
                    git checkout "$FORK_SHA" 2>&1

                    # We need the base commit in the history to compute the diff.
                    # If the fork is a real GitHub fork, it shares history with upstream.
                    # Otherwise, fetch it explicitly.
                    if ! git cat-file -e "$BASE_SHA" 2>/dev/null; then
                        echo "  Base commit not in fork history — fetching from upstream"
                        local UPSTREAM_URL
                        # Derive upstream URL from base_var context or use llama.cpp default
                        UPSTREAM_URL=$(yq ".sources[$i].upstream_repo // \"\"" "$PATCHES_DIR/sources.yaml")
                        if [ -n "$UPSTREAM_URL" ] && [ "$UPSTREAM_URL" != "null" ]; then
                            git remote add upstream "$UPSTREAM_URL" 2>/dev/null || true
                            git fetch upstream 2>&1
                        fi
                    fi

                    local PATCH_COUNT
                    PATCH_COUNT=$(git rev-list --count "$BASE_SHA".."$FORK_SHA" 2>/dev/null || echo "0")
                    echo "  $PATCH_COUNT commits in diff"

                    if [ "$PATCH_COUNT" -gt 0 ]; then
                        mkdir -p "$SOURCE_PATCH_DIR"
                        git format-patch "$BASE_SHA".."$FORK_SHA" -o "$SOURCE_PATCH_DIR/" >/dev/null 2>&1
                        echo "  Generated $PATCH_COUNT patches in patches/$NAME/"
                    fi
                    cd "$SOURCE_DIR"
                else
                    echo "WARNING: Failed to clone $REPO — skipping source '$NAME'"
                fi

                rm -rf "$TMPDIR_CLONE"
            fi
        done
    elif [ -f "$PATCHES_DIR/sources.yaml" ]; then
        echo "WARNING: yq not found — skipping source-based patch generation."
    fi

    # Phase 2: Apply patches (subdirectories first, then top-level)
    for source_dir in $(find "$PATCHES_DIR" -mindepth 1 -maxdepth 1 -type d | sort); do
        for p in $(ls "$source_dir"/*.patch 2>/dev/null | sort); do
            apply_one_patch "$TARGET_DIR" "$p" "$(basename "$source_dir")/$(basename "$p")"
        done
    done
    for p in $(ls "$PATCHES_DIR"/*.patch 2>/dev/null | sort); do
        apply_one_patch "$TARGET_DIR" "$p" "$(basename "$p")"
    done
}

# Run with arguments
if [ $# -lt 2 ]; then
    echo "Usage: $0 <source-dir> <target-dir>"
    exit 1
fi
apply_patches "$1" "$2"
