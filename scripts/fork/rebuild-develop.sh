#!/bin/sh
# Rebuild develop-next from upstream + PR branches + the develop-ci overlay.
#
# Usage:
#   scripts/fork/rebuild-develop.sh            dry run (default): build develop-next locally, print plan
#   scripts/fork/rebuild-develop.sh --push     also push develop-next to origin (never develop itself)
#
# Promotion to `develop` is deliberately not done here: CI builds and tests
# develop-next first, then the rebuild workflow (or a human) promotes it.
#
# On any merge conflict the script stops, prints the conflicting files, removes
# the temporary worktree and exits 1. It never pushes a partial result.

set -eu
. "$(dirname "$0")/lib.sh"

PUSH=0
[ "${1:-}" = "--push" ] && PUSH=1

UP=$(upstream_ref)
UP_REMOTE=$(manifest_get "d['upstream']['remote']")
OVERLAY=$(resolve_ref "$(overlay_branch)")

git fetch --quiet "$UP_REMOTE" || die "fetch $UP_REMOTE failed"
[ -z "$(git status --porcelain --untracked-files=no)" ] || die "working tree has tracked changes; commit or stash first"

WORK=$(mktemp -d "${TMPDIR:-/tmp}/develop-next.XXXXXX")
BRANCH="develop-next-$(date -u +%Y%m%d%H%M%S)"
cleanup() {
    git worktree remove --force "$WORK" 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

git worktree add --quiet -b "$BRANCH" "$WORK" "$UP" || die "could not create worktree"
git -C "$WORK" config rerere.enabled true

cd "$WORK"
UP_SHA=$(git rev-parse HEAD)
echo "base: $UP ($UP_SHA)"

# 1. PR branches, in manifest order. Read from a file, not a pipe, so exit 1 stops the script.
PRS_FILE="$WORK.prs"
pr_branches > "$PRS_FILE"
while IFS="$(printf '\t')" read -r name _pr; do
    ref=$(resolve_ref "$name")
    if git merge-base --is-ancestor "$ref" "$UP_SHA"; then
        echo "skip  $name (already in upstream)"
        continue
    fi
    echo "merge $name ($ref)"
    if ! git merge --no-edit --no-ff -m "Merge PR branch '$name' into develop-next" "$ref" >/dev/null 2>&1; then
        echo "CONFLICT merging $name:" >&2
        git diff --name-only --diff-filter=U >&2 || true
        git merge --abort 2>/dev/null || true
        rm -f "$PRS_FILE"
        exit 1
    fi
done < "$PRS_FILE"
rm -f "$PRS_FILE"

# 2. develop-ci overlay: every commit it adds above upstream, cherry-picked in order.
OVERLAY_BASE=$(git merge-base "$OVERLAY" "$UP")
OVERLAY_COMMITS=$(git rev-list --reverse "$OVERLAY_BASE..$OVERLAY" --no-merges)
for c in $OVERLAY_COMMITS; do
    echo "pick  $(git log -1 --format='%h %s' "$c")"
    if ! git cherry-pick --allow-empty "$c" >/dev/null 2>&1; then
        echo "CONFLICT cherry-picking $c:" >&2
        git diff --name-only --diff-filter=U >&2 || true
        git cherry-pick --abort 2>/dev/null || true
        exit 1
    fi
done

RESULT=$(git rev-parse HEAD)
echo
echo "develop-next: $RESULT"
git log --oneline "$UP_SHA..HEAD" | cat

if [ "$PUSH" = 1 ]; then
    git push --force-with-lease=develop-next origin "HEAD:refs/heads/develop-next"
    echo "pushed origin/develop-next"
else
    echo "dry run: not pushed (use --push)"
fi
