#!/bin/sh
# Report the state of the fork: upstream, PR branches, develop, overlay.
# Read-only. Usage: scripts/fork/status.sh [--json]
#
# Text output is for humans; --json is for skills and workflows.

set -eu
. "$(dirname "$0")/lib.sh"

JSON=0
[ "${1:-}" = "--json" ] && JSON=1

git fetch --quiet "$(manifest_get "d['upstream']['remote']")" 2>/dev/null || true

UP=$(upstream_ref)
UP_SHA=$(git rev-parse "$UP")
OVERLAY=$(overlay_branch)
OVERLAY_REF=$(resolve_ref "$OVERLAY")

DEV_REF=develop
if git show-ref --verify --quiet refs/heads/develop || git show-ref --verify --quiet refs/remotes/origin/develop; then
    DEV_REF=$(resolve_ref develop)
    DEV_SHA=$(git rev-parse "$DEV_REF")
    DEV_BEHIND=$(git rev-list --count "$DEV_REF..$UP")
    DEV_AHEAD=$(git rev-list --count "$UP..$DEV_REF")
else
    DEV_SHA=none; DEV_BEHIND=-; DEV_AHEAD=-
fi

if [ "$JSON" = 1 ]; then
    printf '{"upstream":{"ref":"%s","sha":"%s"},' "$UP" "$UP_SHA"
    printf '"develop":{"ref":"%s","sha":"%s","ahead_of_upstream":"%s","behind_upstream":"%s"},' \
        "$DEV_REF" "$DEV_SHA" "$DEV_AHEAD" "$DEV_BEHIND"
    printf '"overlay":{"ref":"%s","commits_above_upstream":%s},' \
        "$OVERLAY_REF" "$(git rev-list --count "$UP..$OVERLAY_REF")"
    printf '"prs":['
    first=1
    pr_branches | while IFS="$(printf '\t')" read -r name pr; do
        ref=$(resolve_ref "$name")
        ahead=$(git rev-list --count "$UP..$ref")
        behind=$(git rev-list --count "$ref..$UP")
        if git merge-base --is-ancestor "$ref" "$UP" 2>/dev/null; then merged=true; else merged=false; fi
        printf '%s{"name":"%s","ref":"%s","ahead":%s,"behind":%s,"landed_upstream":%s,"upstream_pr":"%s"}' \
            "$([ "$first" = 1 ] || echo ,)" "$name" "$ref" "$ahead" "$behind" "$merged" "$pr"
        first=0
    done
    printf ']}\n'
    exit 0
fi

echo "upstream   $UP  $UP_SHA"
echo "develop    $DEV_REF  $DEV_SHA  (ahead $DEV_AHEAD, behind $DEV_BEHIND upstream)"
echo "overlay    $OVERLAY_REF  ($(git rev-list --count "$UP..$OVERLAY_REF") commits above upstream)"
echo
echo "PR branches:"
pr_branches | while IFS="$(printf '\t')" read -r name pr; do
    ref=$(resolve_ref "$name")
    ahead=$(git rev-list --count "$UP..$ref")
    behind=$(git rev-list --count "$ref..$UP")
    if git merge-base --is-ancestor "$ref" "$UP" 2>/dev/null; then state=landed; else state=open; fi
    printf '  %-34s %-18s ahead %-3s behind %-4s pr=%s\n' "$name" "$state" "$ahead" "$behind" "$pr"
done
