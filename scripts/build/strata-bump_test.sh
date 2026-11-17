#!/bin/bash
# Tests .github/bump_strata.sh offline, against stubbed release data.
#
# Upstream versions each platform on its own and does not attach every package to
# every release. The bump must take every package from the newest release that
# has it, keep the two integrity checks as hard errors, and write nothing when
# nothing changes. A stub gh_curl replaces the network.
set -euo pipefail

REPO_ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

failures=0
fail() {
    echo "FAIL: $*"
    failures=$((failures + 1))
}

sha() { printf '%s' "$1" | sha256sum | cut -d' ' -f1; }
COMMIT=0123456789abcdef0123456789abcdef01234567

# A copy of the script with a stub gh_curl next to it. The stub answers from
# $STUB: releases.json, commit.txt and sums-<tag>.txt.
mkdir -p "$TMP/bin/.github"
cp "$REPO_ROOT/.github/bump_strata.sh" "$TMP/bin/.github/"
cat >"$TMP/bin/.github/gh_curl.sh" <<'STUB'
gh_curl() {
    local url="${*: -1}"
    case "$url" in
    */releases\?per_page=*) cat "$STUB/releases.json" ;;
    */commits/*) cat "$STUB/commit.txt" ;;
    */releases/download/*/SHA256SUMS.txt) cat "$STUB/sums-$(basename "$(dirname "$url")").txt" ;;
    *) echo "unexpected url $url" >&2; return 1 ;;
    esac
}
STUB

# make_stub <dir> <spec...>: each spec is "tag:asset-name,asset-name,..." from
# the newest release to the oldest. The digest and the sum are sha(name), which
# the tests then corrupt on purpose.
make_stub() {
    local dir=$1
    shift
    mkdir -p "$dir"
    echo "$COMMIT" >"$dir/commit.txt"
    python3 -I - "$dir" "$@" <<'PY'
import hashlib, json, sys
d, specs = sys.argv[1], sys.argv[2:]
rels = []
for s in specs:
    tag, names = s.split(":")
    names = [n for n in names.split(",") if n]
    h = lambda n: hashlib.sha256(n.encode()).hexdigest()
    rels.append({"tag_name": tag, "draft": False, "prerelease": False,
                 "assets": [{"name": n, "digest": "sha256:" + h(n)} for n in names]})
    open(f"{d}/sums-{tag}.txt", "w").write("".join(f"{h(n)}  {n}\n" for n in names))
json.dump(rels, open(f"{d}/releases.json", "w"))
PY
}

E=strata-engine
fixture_makefile() {
    cat <<MK
STRATA_VERSION?=v1
STRATA_COMMIT?=${FIX_COMMIT:-1111111111111111111111111111111111111111}
STRATA_ENGINE_TAG_linux_x86_64_cuda_cuda12?=v1
STRATA_ENGINE_VERSION_linux_x86_64_cuda_cuda12?=0.1.59
STRATA_ENGINE_SHA256_linux_x86_64_cuda_cuda12?=$(sha $E-0.1.59-linux-x86_64-cuda-cuda12.tar.gz)
STRATA_ENGINE_TAG_linux_aarch64_cuda_cuda13?=v1
STRATA_ENGINE_VERSION_linux_aarch64_cuda_cuda13?=0.1.59
STRATA_ENGINE_SHA256_linux_aarch64_cuda_cuda13?=$(sha $E-0.1.59-linux-aarch64-cuda-cuda13.tar.gz)
STRATA_ENGINE_TAG_linux_x86_64_sycl?=v1
STRATA_ENGINE_VERSION_linux_x86_64_sycl?=0.1.59
STRATA_ENGINE_SHA256_linux_x86_64_sycl?=$(sha $E-0.1.59-linux-x86_64-sycl.tar.gz)
STRATA_ENGINE_TAG_linux_x86_64_hip_halo?=v0
STRATA_ENGINE_VERSION_linux_x86_64_hip_halo?=0.1.50
STRATA_ENGINE_SHA256_linux_x86_64_hip_halo?=$(printf '%064d' 7)
MK
}

# run_case <name> <stub dir>: runs the script in a fresh work dir with the
# fixture Makefile; leaves out.txt, rc and the work dir for the checks.
run_case() {
    local name=$1 stub=$2
    CASE="$TMP/case-$name"
    mkdir -p "$CASE"
    fixture_makefile >"$CASE/Makefile"
    cp "$CASE/Makefile" "$CASE/Makefile.orig"
    set +e
    (cd "$CASE" && STUB="$stub" bash "$TMP/bin/.github/bump_strata.sh" Niko1221/Strata Makefile STRATA_VERSION >out.txt 2>&1)
    RC=$?
    set -e
}
pin() { grep -E "^$1\?=" "$CASE/Makefile" | cut -d= -f2; }

V1="$E-0.1.59-linux-x86_64-cuda-cuda12.tar.gz,$E-0.1.59-linux-aarch64-cuda-cuda13.tar.gz,$E-0.1.59-linux-x86_64-sycl.tar.gz"

# 1. Mixed versions, a package missing from the newest release, a package in no
# release: v2 has a newer x86 engine, the same aarch64 engine and no sycl package. hip_halo is in no release.
make_stub "$TMP/s1" "v2:$E-0.1.60-linux-x86_64-cuda-cuda12.tar.gz,$E-0.1.59-linux-aarch64-cuda-cuda13.tar.gz" "v1:$V1"
run_case mixed "$TMP/s1"
[ "$RC" = 0 ] || fail "mixed: exit $RC, want 0"
[ "$(pin STRATA_VERSION)" = v2 ] || fail "mixed: STRATA_VERSION is $(pin STRATA_VERSION)"
[ "$(pin STRATA_COMMIT)" = "$COMMIT" ] || fail "mixed: STRATA_COMMIT not updated"
[ "$(pin STRATA_ENGINE_TAG_linux_x86_64_cuda_cuda12)" = v2 ] || fail "mixed: x86 tag"
[ "$(pin STRATA_ENGINE_VERSION_linux_x86_64_cuda_cuda12)" = 0.1.60 ] || fail "mixed: x86 version"
[ "$(pin STRATA_ENGINE_SHA256_linux_x86_64_cuda_cuda12)" = "$(sha $E-0.1.60-linux-x86_64-cuda-cuda12.tar.gz)" ] || fail "mixed: x86 sha"
[ "$(pin STRATA_ENGINE_TAG_linux_aarch64_cuda_cuda13)" = v2 ] || fail "mixed: aarch64 tag"
[ "$(pin STRATA_ENGINE_VERSION_linux_aarch64_cuda_cuda13)" = 0.1.59 ] || fail "mixed: aarch64 version"
# Missing from the newest release: taken from the older one, with a notice.
[ "$(pin STRATA_ENGINE_TAG_linux_x86_64_sycl)" = v1 ] || fail "mixed: sycl tag moved"
grep -q '::notice.*linux-x86_64-sycl' "$CASE/out.txt" || fail "mixed: no notice for the package taken from an older release"
# In no release: the pin stays and there is a warning.
[ "$(pin STRATA_ENGINE_TAG_linux_x86_64_hip_halo)" = v0 ] || fail "no release: tag changed"
[ "$(pin STRATA_ENGINE_VERSION_linux_x86_64_hip_halo)" = 0.1.50 ] || fail "no release: version changed"
[ "$(pin STRATA_ENGINE_SHA256_linux_x86_64_hip_halo)" = "$(printf '%064d' 7)" ] || fail "no release: sha changed"
grep -q '::warning.*linux-x86_64-hip-halo' "$CASE/out.txt" || fail "no release: no warning"
# Output files the workflow reads.
grep -q "^v2 ($COMMIT)\$" "$CASE/STRATA_VERSION_commit.txt" || fail "mixed: commit file"
grep -q 'linux_x86_64_cuda_cuda12.: v1 / 0.1.59 -> v2 / 0.1.60' "$CASE/STRATA_VERSION_message.txt" || fail "mixed: message misses the x86 change"
grep -q 'linux_aarch64_cuda_cuda13.: v1 / 0.1.59 -> v2 / 0.1.59' "$CASE/STRATA_VERSION_message.txt" || fail "mixed: message misses the aarch64 tag move"
! grep -q 'sycl' "$CASE/STRATA_VERSION_message.txt" || fail "mixed: message lists an unchanged package"

# 2. A second run on the result changes nothing and writes no output file.
cp "$CASE/Makefile" "$TMP/after-mixed"
(cd "$CASE" && rm -f STRATA_VERSION_*.txt && STUB="$TMP/s1" bash "$TMP/bin/.github/bump_strata.sh" Niko1221/Strata Makefile STRATA_VERSION >out2.txt 2>&1) || fail "rerun: nonzero exit"
cmp -s "$CASE/Makefile" "$TMP/after-mixed" || fail "rerun: Makefile changed"
ls "$CASE"/STRATA_VERSION_*.txt >/dev/null 2>&1 && fail "rerun: wrote an output file"

# 3. Nothing changed at all (the newest release and its commit are the pinned ones).
make_stub "$TMP/s3" "v1:$V1"
FIX_COMMIT=$COMMIT run_case same "$TMP/s3"
[ "$RC" = 0 ] || fail "no change: exit $RC"
ls "$CASE"/STRATA_VERSION_*.txt >/dev/null 2>&1 && fail "no change: wrote an output file"
cmp -s "$CASE/Makefile" "$CASE/Makefile.orig" || fail "no change: Makefile changed"

# 4. A digest mismatch is a hard error, in either guard, and leaves the Makefile alone.
make_stub "$TMP/s4" "v2:$E-0.1.60-linux-x86_64-cuda-cuda12.tar.gz" "v1:$V1"
sed -i "s/^[0-9a-f]\{64\}  $E-0.1.60/$(printf '%064d' 1)  $E-0.1.60/" "$TMP/s4/sums-v2.txt"
run_case sums-mismatch "$TMP/s4"
[ "$RC" = 1 ] || fail "SHA256SUMS mismatch: exit $RC, want 1"
cmp -s "$CASE/Makefile" "$CASE/Makefile.orig" || fail "SHA256SUMS mismatch: Makefile changed"
ls "$CASE"/STRATA_VERSION_*.txt >/dev/null 2>&1 && fail "SHA256SUMS mismatch: wrote an output file"

make_stub "$TMP/s5" "v2:$E-0.1.60-linux-x86_64-cuda-cuda12.tar.gz" "v1:$V1"
python3 -I - "$TMP/s5/releases.json" <<'PY'
import json, sys
p = sys.argv[1]
r = json.load(open(p))
r[0]["assets"][0]["digest"] = "sha256:" + "2" * 64
json.dump(r, open(p, "w"))
PY
run_case digest-mismatch "$TMP/s5"
[ "$RC" = 1 ] || fail "asset digest mismatch: exit $RC, want 1"
cmp -s "$CASE/Makefile" "$CASE/Makefile.orig" || fail "asset digest mismatch: Makefile changed"

if [ "$failures" -gt 0 ]; then
    echo "$failures failure(s)"
    exit 1
fi
echo "ok"
