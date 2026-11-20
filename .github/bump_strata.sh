#!/bin/bash
# Bump the Strata pins in backend/go/strata/Makefile.
#
# The strata backend does not build the engine. It fetches the engine packages
# upstream publishes with its releases. Upstream versions each platform on its
# own and does not attach every package to every release, so the Makefile pins
# every package separately: its release tag, its version and its SHA-256. This
# script moves those pins, plus STRATA_VERSION and STRATA_COMMIT (the newest
# release and its commit; the Python modules come from it). bump_deps.sh only
# handles one commit variable, hence this script.
#
# For each package the script takes the newest release that contains it. Mixed
# versions and tags across packages are normal. A package that is in no release
# keeps its pin and raises a warning.
#
# Each package's SHA-256 comes from the SHA256SUMS.txt of the release the
# package is taken from and must equal the digest GitHub computed for the
# uploaded asset. Both checks are hard errors. The pull request this opens is
# the review step; nothing is trusted after that except those pins.
#
# When nothing changes the script writes no output file, and the workflow opens
# no pull request.
set -xe

source "$(dirname "${BASH_SOURCE[0]}")/gh_curl.sh"

REPO=$1   # Niko1221/Strata
FILE=$2   # backend/go/strata/Makefile
VAR=$3    # STRATA_VERSION (used for the output file names the workflow reads)

if [ -z "$REPO" ] || [ -z "$FILE" ] || [ -z "$VAR" ]; then
    echo "usage: $0 <repo> <makefile> <var-name>" >&2
    exit 1
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Newest first. Drafts and pre-releases are not candidates.
gh_curl -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/$REPO/releases?per_page=30" > "$WORK/releases.json"

# Step 1: for every package pinned in the Makefile, the newest release that
# contains it. Writes plan.tsv (family, package, tag, version, asset name,
# GitHub digest) and tags.txt (the releases the SHA256SUMS.txt files are needed
# for). The first line of tags.txt is the newest release.
python3 -I - "$FILE" "$WORK" <<'PY'
import json, re, sys

makefile, work = sys.argv[1:3]
releases = [r for r in json.load(open(f"{work}/releases.json"))
            if not r.get("draft") and not r.get("prerelease")]
if not releases:
    sys.exit("Refusing to bump: no release found")

text = open(makefile).read()
families = {"ENGINE": "strata-engine"}
plan, tags = [], [releases[0]["tag_name"]]
for fam, prefix in families.items():
    for pkg in re.findall(rf"^STRATA_{fam}_SHA256_([a-z0-9_]+)\?=", text, re.M):
        # linux_x86_64_cuda_cuda12 -> linux-x86_64-cuda-cuda12
        suffix = pkg.replace("_", "-").replace("x86-64", "x86_64")
        pat = re.compile(rf"^{prefix}-([0-9][0-9.]*)-{re.escape(suffix)}\.tar\.gz$")
        found = None
        for i, rel in enumerate(releases):
            hits = [(a["name"], pat.match(a["name"]).group(1), a.get("digest") or "")
                    for a in rel.get("assets", []) if pat.match(a["name"])]
            if len(hits) > 1:
                sys.exit(f"Refusing to bump: {rel['tag_name']} has several {prefix} packages for {suffix}")
            if hits:
                found = (rel["tag_name"], i, *hits[0])
                break
        if not found:
            print(f"::warning title=Strata package not found::no release contains a {prefix} package for {suffix}; its pin is unchanged")
            continue
        tag, idx, name, ver, digest = found
        if idx > 0:
            print(f"::notice title=Strata package from an older release::{name} is not in {releases[0]['tag_name']}; taking it from {tag}")
        plan.append("\t".join([fam, pkg, tag, ver, name, digest.removeprefix("sha256:")]))
        if tag not in tags:
            tags.append(tag)
open(f"{work}/plan.tsv", "w").write("\n".join(plan) + ("\n" if plan else ""))
open(f"{work}/tags.txt", "w").write("\n".join(tags) + "\n")
PY

LATEST=$(head -n1 "$WORK/tags.txt")

# The commit the newest tag points at (the API resolves annotated tags).
COMMIT=$(gh_curl -H "Accept: application/vnd.github.VERSION.sha" \
    "https://api.github.com/repos/$REPO/commits/$LATEST" || true)
if ! [[ "$COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
    echo "Refusing to bump: expected a 40-char commit SHA for $REPO@$LATEST, got: $COMMIT" >&2
    exit 1
fi

# The checksum file of every release a package comes from.
mkdir -p "$WORK/sums"
while read -r tag; do
    gh_curl -L "https://github.com/$REPO/releases/download/$tag/SHA256SUMS.txt" > "$WORK/sums/$tag"
done < "$WORK/tags.txt"

# Step 2: verify every package, then rewrite the Makefile in one go, so a failed
# check never leaves a half-rewritten file. Writes message.txt when something
# changed.
python3 -I - "$FILE" "$WORK" "$REPO" "$LATEST" "$COMMIT" <<'PY'
import os, re, sys

makefile, work, repo, latest, commit = sys.argv[1:6]
text = open(makefile).read()
orig = text

def get(name):
    m = re.search(rf"^{name}\?=(.*)$", text, re.M)
    return m.group(1).strip() if m else None

def put(name, value):
    global text
    text, n = re.subn(rf"^{name}\?=.*$", lambda _: f"{name}?={value}", text, flags=re.M)
    if n != 1:
        sys.exit(f"Could not find {name} in {makefile}")

sums = {}
for tag in os.listdir(f"{work}/sums"):
    sums[tag] = open(f"{work}/sums/{tag}").read().splitlines()

changes = []
for row in open(f"{work}/plan.tsv").read().splitlines():
    fam, pkg, tag, ver, name, digest = row.split("\t")
    lines = [l for l in sums[tag] if re.fullmatch(rf"[0-9a-f]{{64}}  {re.escape(name)}", l)]
    if len(lines) != 1:
        sys.exit(f"Refusing to bump: SHA256SUMS.txt of {tag} has {len(lines)} entries for {name}")
    sha = lines[0].split()[0]
    # GitHub's own digest for the uploaded asset must agree with SHA256SUMS.txt.
    if digest != sha:
        sys.exit(f"Refusing to bump: SHA256SUMS.txt and the GitHub asset digest disagree for {name} ({sha} vs '{digest}')")
    old = (get(f"STRATA_{fam}_TAG_{pkg}"), get(f"STRATA_{fam}_VERSION_{pkg}"), get(f"STRATA_{fam}_SHA256_{pkg}"))
    if old != (tag, ver, sha):
        label = "engine"
        changes.append(f"- {label} `{pkg}`: {old[0]} / {old[1]} -> {tag} / {ver}")
        put(f"STRATA_{fam}_TAG_{pkg}", tag)
        put(f"STRATA_{fam}_VERSION_{pkg}", ver)
        put(f"STRATA_{fam}_SHA256_{pkg}", sha)

current_tag = get("STRATA_VERSION")
put("STRATA_VERSION", latest)
put("STRATA_COMMIT", commit)

if text == orig:
    print("Strata pins are up to date; nothing to do")
    sys.exit(0)

open(makefile, "w").write(text)
msg = [f"Changes: https://github.com/{repo}/compare/{current_tag}...{latest}", ""]
msg.append(f"Newest release: {latest} ({commit}). The Python modules are fetched at that commit.")
msg.append("")
if changes:
    msg.append("Packages are pinned one by one; each comes from the newest release that has it, so tags and versions can differ between packages.")
    msg.append("")
    msg += changes
else:
    msg.append("No package pin changed.")
open(f"{work}/message.txt", "w").write("\n".join(msg) + "\n")
PY

if [ ! -s "$WORK/message.txt" ]; then
    exit 0
fi

cp "$WORK/message.txt" "${VAR}_message.txt"
echo "${LATEST} (${COMMIT})" > "${VAR}_commit.txt"
