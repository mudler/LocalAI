#!/bin/sh
# SPDX-License-Identifier: MIT
# Renders the library index of the first-party add-ons: one entry for each
# enabled platform of each add-on (addons/<id>/package/platforms.yaml), from
# the template gallery/addons/<id>.yaml.tmpl.
#
# Development index, for gallery/addons/index.yaml (mutable entries, no digest,
# signed by the master run of the package workflow). It needs no built package:
#
#   sh scripts/addons/catalog.sh --channel development --repository <repo> --out <file>
#   sh scripts/addons/catalog.sh --channel development --check
#
# Release index, for the asset addons-index.yaml of a GitHub release (entries
# pinned by digest and tag, signed by the run of the tag). It reads the merged
# layout of each add-on, <merged-root>/<id>:
#
#   sh scripts/addons/catalog.sh --channel release --repository <repo> \
#       --version <semver> --signer-ref refs/tags/v<semver> \
#       --merged-root <dir> --out <file>
#
# --check (the repository defaults to the one of the committed index) compares the committed gallery/addons/index.yaml with the generated
# development index and fails when they differ. --owner-repo defaults to
# mudler/LocalAI.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
channel= repository= version= signer_ref= merged_root= out= check= owner_repo=mudler/LocalAI
while [ "$#" -gt 0 ]; do
    case "$1" in
    --channel) channel=${2-}; shift 2 ;;
    --repository) repository=${2-}; shift 2 ;;
    --version) version=${2-}; shift 2 ;;
    --signer-ref) signer_ref=${2-}; shift 2 ;;
    --merged-root) merged_root=${2-}; shift 2 ;;
    --owner-repo) owner_repo=${2-}; shift 2 ;;
    --out) out=${2-}; shift 2 ;;
    --check) check=1; shift ;;
    *) echo "catalog.sh: unknown argument $1" >&2; exit 2 ;;
    esac
done
if [ -z "$repository" ] && [ -n "$check" ]; then
    # A check without a repository uses the one the committed index names.
    repository=$(sed -n 's|^  artifact: "oci://\([^:"]*\):.*|\1|p' gallery/addons/index.yaml | head -n 1)
fi
[ -n "$repository" ] || { echo 'catalog.sh: --repository is required' >&2; exit 2; }
case "$channel" in
development)
    signer_ref=refs/heads/master
    ;;
release)
    [ -n "$version" ] && [ -n "$signer_ref" ] && [ -n "$merged_root" ] || {
        echo 'catalog.sh: the release channel needs --version, --signer-ref and --merged-root' >&2
        exit 2
    }
    ;;
*) echo 'catalog.sh: --channel is development or release' >&2; exit 2 ;;
esac
[ -n "$out" ] || [ -n "$check" ] || { echo 'catalog.sh: --out or --check is required' >&2; exit 2; }
scratch=$(mktemp -d "${TMPDIR:-/tmp}/addon-catalog.XXXXXX")
trap 'rm -rf "$scratch"' EXIT HUP INT TERM
tool=${ADDONPKG:-}
if [ -z "$tool" ]; then
    go build -o "$scratch/addonpkg" ./scripts/addons/cmd/addonpkg
    tool="$scratch/addonpkg"
fi
result=$scratch/index.yaml
if [ "$channel" = development ]; then
    cat > "$result" <<'HEADER'
# Official add-on library: the first-party add-ons, one entry for each
# enabled platform of each package. LocalAI registers this file as its default
# library (the raw URL of this file on the master branch). The file is
# generated, never edited by hand:
#   sh scripts/addons/catalog.sh --channel development --repository <repo> --out gallery/addons/index.yaml
# CI fails when the committed file differs from the generated one.
# Every entry is a development entry: it is mutable, keeps the moving tag
# <id>-development and has no digest. The install resolves the tag to a digest,
# verifies the signature against it and records it, as for a development
# backend. A tagged release does not read this file for its pins: the release
# workflow renders pinned entries into the asset addons-index.yaml of the
# release (see .agents/preparing-a-release.md).
HEADER
else
    : > "$result"
fi
for tmpl in gallery/addons/*.yaml.tmpl; do
    id=$(basename "$tmpl" .yaml.tmpl)
    config=addons/$id/package/platforms.yaml
    part=$scratch/$id.yaml
    if [ "$channel" = development ]; then
        "$tool" catalog --config "$config" --template "$tmpl" --from-manifest \
            --repository "$repository" --channel development \
            --owner-repo "$owner_repo" --signer-ref "$signer_ref" --out "$part" > /dev/null
    else
        "$tool" catalog --config "$config" --template "$tmpl" --merged "$merged_root/$id" \
            --repository "$repository" --tag "$version" --channel latest \
            --owner-repo "$owner_repo" --signer-ref "$signer_ref" --out "$part" > /dev/null
    fi
    cat "$part" >> "$result"
done
if [ -n "$check" ]; then
    if ! diff -u gallery/addons/index.yaml "$result"; then
        echo 'catalog.sh: gallery/addons/index.yaml is stale; regenerate it with --out gallery/addons/index.yaml' >&2
        exit 1
    fi
    exit 0
fi
mkdir -p "$(dirname "$out")"
cp "$result" "$out"
