#!/bin/bash
# Bump the colibri pins in backend/go/colibri/Makefile.
#
# The colibri backend fetches the release archives upstream publishes and, for
# the Linux CUDA images, compiles the engines from the source of the same
# release. The Makefile pins:
#   COLIBRI_VERSION          the release tag
#   COLIBRI_COMMIT           the commit of that tag (the CUDA build checks out
#                            this commit and refuses a tag that points elsewhere)
#   COLIBRI_SHA256_<plat>    the SHA-256 of each platform's archive
# They move together, in one pull request, so `make check-pins` and the CUDA
# build's tag check stay green. bump_deps.sh handles one commit variable only,
# hence this script.
#
# Each SHA-256 comes from the release's SHA256SUMS.txt and must equal the digest
# GitHub computed for the uploaded asset; both checks are hard errors. A
# platform whose archive is missing from the release is a hard error too: the
# pins must stay one release.
#
# Environment:
#   COLIBRI_BUMP_TAG  bump to this tag instead of the newest release
#   DRY_RUN=1         print the pins the bump would write and change nothing
#
# When nothing changes the script writes no output file, and the workflow opens
# no pull request.
set -xe

source "$(dirname "${BASH_SOURCE[0]}")/gh_curl.sh"

REPO=$1   # JustVugg/colibri
FILE=$2   # backend/go/colibri/Makefile
VAR=$3    # COLIBRI_VERSION (used for the output file names the workflow reads)

if [ -z "$REPO" ] || [ -z "$FILE" ] || [ -z "$VAR" ]; then
    echo "usage: $0 <repo> <makefile> <var-name>" >&2
    exit 1
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

if [ -n "${COLIBRI_BUMP_TAG:-}" ]; then
    gh_curl -H "Accept: application/vnd.github+json" \
        "https://api.github.com/repos/$REPO/releases/tags/$COLIBRI_BUMP_TAG" > "$WORK/release.json"
else
    # The newest release that is not a draft or a pre-release.
    gh_curl -H "Accept: application/vnd.github+json" \
        "https://api.github.com/repos/$REPO/releases/latest" > "$WORK/release.json"
fi

TAG=$(python3 -I -c 'import json,sys; r=json.load(open(sys.argv[1])); print(r["tag_name"])' "$WORK/release.json")
if ! [[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Refusing to bump: unexpected release tag '$TAG'" >&2
    exit 1
fi

# The commit the tag points at (the API resolves annotated tags).
COMMIT=$(gh_curl -H "Accept: application/vnd.github.VERSION.sha" \
    "https://api.github.com/repos/$REPO/commits/$TAG" || true)
if ! [[ "$COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
    echo "Refusing to bump: expected a 40-char commit SHA for $REPO@$TAG, got: $COMMIT" >&2
    exit 1
fi

gh_curl -L "https://github.com/$REPO/releases/download/$TAG/SHA256SUMS.txt" > "$WORK/sums.txt"

# Verify every pinned platform, then rewrite the Makefile in one go, so a failed
# check never leaves a half-rewritten file. Writes message.txt when something
# changed.
python3 -I - "$FILE" "$WORK" "$REPO" "$TAG" "$COMMIT" "${DRY_RUN:-0}" <<'PY'
import json, re, sys

makefile, work, repo, tag, commit, dry = sys.argv[1:7]
text = open(makefile).read()
orig = text
release = json.load(open(f"{work}/release.json"))
digests = {a["name"]: (a.get("digest") or "").removeprefix("sha256:") for a in release.get("assets", [])}
sums = open(f"{work}/sums.txt").read().splitlines()

def get(name):
    m = re.search(rf"^{name}\?=(.*)$", text, re.M)
    return m.group(1).strip() if m else None

def put(name, value):
    global text
    text, n = re.subn(rf"^{name}\?=.*$", lambda _: f"{name}?={value}", text, flags=re.M)
    if n != 1:
        sys.exit(f"Could not find {name} in {makefile}")

plats = re.findall(r"^COLIBRI_SHA256_([a-z0-9_]+)\?=", text, re.M)
if not plats:
    sys.exit(f"No COLIBRI_SHA256_<platform> pins in {makefile}")
new = {"COLIBRI_VERSION": tag, "COLIBRI_COMMIT": commit}
for plat in plats:
    # linux_x86_64 -> linux-x86_64, macos_arm64 -> macos-arm64
    os_, arch = plat.split("_", 1)
    name = f"colibri-{tag}-{os_}-{arch}.tar.gz"
    lines = [l for l in sums if re.fullmatch(rf"[0-9a-f]{{64}}  {re.escape(name)}", l)]
    if len(lines) != 1:
        sys.exit(f"Refusing to bump: SHA256SUMS.txt of {tag} has {len(lines)} entries for {name}")
    sha = lines[0].split()[0]
    # GitHub's own digest for the uploaded asset must agree with SHA256SUMS.txt.
    if name not in digests:
        sys.exit(f"Refusing to bump: release {tag} has no asset {name}")
    if digests[name] != sha:
        sys.exit(f"Refusing to bump: SHA256SUMS.txt and the GitHub asset digest disagree for {name} ({sha} vs '{digests[name]}')")
    new[f"COLIBRI_SHA256_{plat}"] = sha

if dry == "1":
    for k, v in new.items():
        print(f"{k}?={v}")
    sys.exit(0)

current = get("COLIBRI_VERSION")
for k, v in new.items():
    put(k, v)

if text == orig:
    print("colibri pins are up to date; nothing to do")
    sys.exit(0)

open(makefile, "w").write(text)
msg = [f"Changes: https://github.com/{repo}/compare/{current}...{tag}", "",
       f"Release {tag} ({commit}). The archives' SHA-256 come from the release's SHA256SUMS.txt",
       "and match GitHub's asset digests. The Linux CUDA images compile the engines from this commit.", ""]
msg += [f"- `{k}`: `{v}`" for k, v in new.items()]
open(f"{work}/message.txt", "w").write("\n".join(msg) + "\n")
PY

if [ ! -s "$WORK/message.txt" ]; then
    exit 0
fi

cp "$WORK/message.txt" "${VAR}_message.txt"
echo "${TAG} (${COMMIT})" > "${VAR}_commit.txt"
