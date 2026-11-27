#!/bin/bash
# Tests .github/bump_colibri.sh offline, against stubbed release data, on a copy
# of the real backend/go/colibri/Makefile.
#
# The bump must move the release tag, its commit and every platform's SHA-256
# together, so that `make check-pins` stays green; it must reproduce the current
# pins from the current release's data; it must keep the two integrity checks
# as hard errors; and it must write nothing when nothing changes. A stub
# gh_curl replaces the network.
set -euo pipefail

REPO_ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")
MAKEFILE="$REPO_ROOT/backend/go/colibri/Makefile"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

failures=0
fail() {
    echo "FAIL: $*"
    failures=$((failures + 1))
}

pin_of() { grep -E "^$1\?=" "$2" | cut -d= -f2; }
PLATS="linux_x86_64 linux_aarch64 macos_arm64"

# A copy of the script with a stub gh_curl next to it. The stub answers from
# $STUB: release.json, commit.txt and sums.txt.
mkdir -p "$TMP/bin/.github"
cp "$REPO_ROOT/.github/bump_colibri.sh" "$TMP/bin/.github/"
cat >"$TMP/bin/.github/gh_curl.sh" <<'STUB'
gh_curl() {
    local url="${*: -1}"
    case "$url" in
    */releases/latest|*/releases/tags/*) cat "$STUB/release.json" ;;
    */commits/*) cat "$STUB/commit.txt" ;;
    */releases/download/*/SHA256SUMS.txt) cat "$STUB/sums.txt" ;;
    *) echo "unexpected url $url" >&2; return 1 ;;
    esac
}
STUB

# make_stub <dir> <tag> <commit> <plat=sha...>: a release with one archive per
# platform given, and a SHA256SUMS.txt and GitHub digests that agree.
make_stub() {
    local dir=$1 tag=$2 commit=$3
    shift 3
    mkdir -p "$dir"
    echo "$commit" >"$dir/commit.txt"
    python3 -I - "$dir" "$tag" "$@" <<'PY'
import json, sys
d, tag, pairs = sys.argv[1], sys.argv[2], sys.argv[3:]
assets, sums = [], []
for p in pairs:
    plat, sha = p.split("=")
    os_, arch = plat.split("_", 1)
    name = f"colibri-{tag}-{os_}-{arch}.tar.gz"
    assets.append({"name": name, "digest": "sha256:" + sha})
    sums.append(f"{sha}  {name}\n")
assets.append({"name": "SHA256SUMS.txt", "digest": "sha256:" + "0" * 64})
json.dump({"tag_name": tag, "draft": False, "prerelease": False, "assets": assets}, open(f"{d}/release.json", "w"))
open(f"{d}/sums.txt", "w").write("".join(sums))
PY
}

# run_case <name> <stub dir> [env...]: runs the script on a copy of the real
# Makefile; leaves out.txt, RC and CASE for the checks.
run_case() {
    local name=$1 stub=$2
    shift 2
    CASE="$TMP/case-$name"
    mkdir -p "$CASE"
    cp "$MAKEFILE" "$CASE/Makefile"
    cp "$CASE/Makefile" "$CASE/Makefile.orig"
    set +e
    (cd "$CASE" && env STUB="$stub" "$@" bash "$TMP/bin/.github/bump_colibri.sh" JustVugg/colibri Makefile COLIBRI_VERSION >out.txt 2>&1)
    RC=$?
    set -e
}

# The data of the release the Makefile pins now, read from the Makefile itself.
CUR_TAG=$(pin_of COLIBRI_VERSION "$MAKEFILE")
CUR_COMMIT=$(pin_of COLIBRI_COMMIT "$MAKEFILE")
CUR=()
for p in $PLATS; do CUR+=("$p=$(pin_of "COLIBRI_SHA256_$p" "$MAKEFILE")"); done

# 1. The current release's data reproduces the current pins: no change, no
# output file, and the dry run prints exactly the pinned values.
make_stub "$TMP/cur" "$CUR_TAG" "$CUR_COMMIT" "${CUR[@]}"
run_case same "$TMP/cur"
[ "$RC" = 0 ] || fail "same: exit $RC, want 0"
cmp -s "$CASE/Makefile" "$CASE/Makefile.orig" || fail "same: Makefile changed"
ls "$CASE"/COLIBRI_VERSION_*.txt >/dev/null 2>&1 && fail "same: wrote an output file"
run_case dry "$TMP/cur" DRY_RUN=1
[ "$RC" = 0 ] || fail "dry run: exit $RC"
grep -E '^COLIBRI_(VERSION|COMMIT|SHA256_[a-z0-9_]+)\?=' "$CASE/out.txt" | sort >"$TMP/dry.txt"
grep -E '^COLIBRI_(VERSION|COMMIT|SHA256_[a-z0-9_]+)\?=' "$MAKEFILE" | sort >"$TMP/pins.txt"
cmp -s "$TMP/dry.txt" "$TMP/pins.txt" || fail "dry run: does not reproduce the pins: $(diff "$TMP/dry.txt" "$TMP/pins.txt" | tr '\n' ' ')"
cmp -s "$CASE/Makefile" "$CASE/Makefile.orig" || fail "dry run: Makefile changed"

# 2. A new release moves every pin together, and check-pins passes on the result.
NEW_COMMIT=0123456789abcdef0123456789abcdef01234567
NEW=()
i=1
for p in $PLATS; do NEW+=("$p=$(printf '%064x' $i)"); i=$((i + 1)); done
make_stub "$TMP/new" v9.9.9 "$NEW_COMMIT" "${NEW[@]}"
run_case bump "$TMP/new"
[ "$RC" = 0 ] || fail "bump: exit $RC, want 0"
[ "$(pin_of COLIBRI_VERSION "$CASE/Makefile")" = v9.9.9 ] || fail "bump: COLIBRI_VERSION not moved"
[ "$(pin_of COLIBRI_COMMIT "$CASE/Makefile")" = "$NEW_COMMIT" ] || fail "bump: COLIBRI_COMMIT not moved"
i=1
for p in $PLATS; do
    [ "$(pin_of "COLIBRI_SHA256_$p" "$CASE/Makefile")" = "$(printf '%064x' $i)" ] || fail "bump: COLIBRI_SHA256_$p not moved"
    i=$((i + 1))
done
make -s -C "$CASE" check-pins >/dev/null 2>&1 || fail "bump: check-pins fails on the bumped Makefile"
grep -q "^v9.9.9 ($NEW_COMMIT)\$" "$CASE/COLIBRI_VERSION_commit.txt" || fail "bump: commit file"
grep -q "compare/$CUR_TAG...v9.9.9" "$CASE/COLIBRI_VERSION_message.txt" || fail "bump: message has no compare link"
# Nothing else in the Makefile moved.
diff <(grep -vE '^COLIBRI_(VERSION|COMMIT|SHA256_[a-z0-9_]+)\?=' "$CASE/Makefile.orig") \
     <(grep -vE '^COLIBRI_(VERSION|COMMIT|SHA256_[a-z0-9_]+)\?=' "$CASE/Makefile") >/dev/null || fail "bump: changed other lines"

# 3. A second run on the result changes nothing and writes no output file.
cp "$CASE/Makefile" "$TMP/after-bump"
(cd "$CASE" && rm -f COLIBRI_VERSION_*.txt && STUB="$TMP/new" bash "$TMP/bin/.github/bump_colibri.sh" JustVugg/colibri Makefile COLIBRI_VERSION >out2.txt 2>&1) || fail "rerun: nonzero exit"
cmp -s "$CASE/Makefile" "$TMP/after-bump" || fail "rerun: Makefile changed"
ls "$CASE"/COLIBRI_VERSION_*.txt >/dev/null 2>&1 && fail "rerun: wrote an output file"

# 4. SHA256SUMS.txt and the asset digest disagree: hard error, nothing written.
make_stub "$TMP/sums" v9.9.9 "$NEW_COMMIT" "${NEW[@]}"
sed -i "1s/^[0-9a-f]\{64\}/$(printf '%064d' 5)/" "$TMP/sums/sums.txt"
run_case sums-mismatch "$TMP/sums"
[ "$RC" = 1 ] || fail "SHA256SUMS mismatch: exit $RC, want 1"
cmp -s "$CASE/Makefile" "$CASE/Makefile.orig" || fail "SHA256SUMS mismatch: Makefile changed"
ls "$CASE"/COLIBRI_VERSION_*.txt >/dev/null 2>&1 && fail "SHA256SUMS mismatch: wrote an output file"

# 5. A platform's archive is missing from the release: hard error, the pins
# stay one release.
make_stub "$TMP/missing" v9.9.9 "$NEW_COMMIT" "${NEW[@]:0:2}"
run_case missing "$TMP/missing"
[ "$RC" = 1 ] || fail "missing platform: exit $RC, want 1"
cmp -s "$CASE/Makefile" "$CASE/Makefile.orig" || fail "missing platform: Makefile changed"

# 6. The tag does not resolve to a commit: hard error.
make_stub "$TMP/nocommit" v9.9.9 "not-a-commit" "${NEW[@]}"
run_case no-commit "$TMP/nocommit"
[ "$RC" = 1 ] || fail "bad commit: exit $RC, want 1"
cmp -s "$CASE/Makefile" "$CASE/Makefile.orig" || fail "bad commit: Makefile changed"

if [ "$failures" -gt 0 ]; then
    echo "$failures failure(s)"
    exit 1
fi
echo "ok"
