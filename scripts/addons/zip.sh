#!/bin/sh
# SPDX-License-Identifier: MIT
# Writes the add-on zip files that "Install from a file" takes: one zip and one
# .sha256 file per add-on and platform. It is not a second build path: every zip
# is made from the verified OCI layout that package.sh builds, so it holds
# the same executable, UI, icon, skills and manifest as the registry package.
#
#   sh scripts/addons/zip.sh [--addon <id>] [--platforms <os/arch,...>]
#       [--out <dir>] [--version <semver>] [--executables <dir>]
#       [--runtime-dir <dir>] [--local-check]
#
# Without --addon every add-on that has a platform file is built, except the
# Sandbox add-on when SANDBOX_GUEST_IMAGE is not set (its executable needs the
# reference of its guest image; see package.sh). Without --platforms the
# platform of this host is built. A platform other than this host needs the
# native executable: put it at <executables>/<os>-<arch>/<executable name>.
# --local-check allows a platform that the platform matrix has not enabled.
# --version defaults to the version in the manifest of the add-on.
#
# The files are written to --out, dist/addons by default:
#   <id>-<version>-<os>-<arch>.zip
#   <id>-<version>-<os>-<arch>.zip.sha256   (sha256sum format)
# Install one with:  local-ai addons install dist/addons/<name>.zip
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
addon= platforms= out=dist/addons version= executables= runtime_dir= local_check=
while [ "$#" -gt 0 ]; do
    case "$1" in
    --addon) addon=${2-}; shift 2 ;;
    --platforms) platforms=${2-}; shift 2 ;;
    --out) out=${2-}; shift 2 ;;
    --version) version=${2-}; shift 2 ;;
    --executables) executables=${2-}; shift 2 ;;
    --runtime-dir) runtime_dir=${2-}; shift 2 ;;
    --local-check) local_check=1; shift ;;
    *) echo "zip.sh: unknown argument $1" >&2; exit 2 ;;
    esac
done
host="$(go env GOOS)/$(go env GOARCH)"
[ -n "$platforms" ] || platforms=$host
# Work files live under the build cache of the checkout, never in /tmp.
work_root=${ADDON_ZIP_WORK:-$root/.cache/addons/zip}
mkdir -p "$work_root" "$out"
work=$(mktemp -d "$work_root/run.XXXXXX")
trap 'chmod -R u+rwx "$work" 2>/dev/null; rm -rf "$work"' EXIT HUP INT TERM
go build -o "$work/addonpkg" ./scripts/addons/cmd/addonpkg
export ADDONPKG="$work/addonpkg"

if [ -n "$addon" ]; then
    case "$addon" in '' | *[!a-z0-9-]*) echo "zip.sh: --addon must be an add-on ID" >&2; exit 2 ;; esac
    [ -f "addons/$addon/package/platforms.yaml" ] || { echo "zip.sh: no add-on $addon has a platform file" >&2; exit 2; }
    list="$addon"
else
    list=
    for f in addons/*/package/platforms.yaml; do
        a=$(sed -n 's/^addon: //p' "$f")
        if [ "$a" = sandbox ] && [ -z "${SANDBOX_GUEST_IMAGE-}" ]; then
            echo "zip.sh: skipping sandbox: set SANDBOX_GUEST_IMAGE=<repository>@sha256:<digest> to build it" >&2
            continue
        fi
        list="$list $a"
    done
fi

built=0
for a in $list; do
    config="addons/$a/package/platforms.yaml"
    exe_name=$(sed -n 's/^executable: //p' "$config")
    v=$version
    [ -n "$v" ] || v=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "addons/$a/manifest.json" | head -1)
    [ -n "$v" ] || { echo "zip.sh: no version for $a; pass --version" >&2; exit 2; }
    for p in $(echo "$platforms" | tr ',' ' '); do
        layout="$work/$a-$(echo "$p" | tr '/' '-')"
        set -- --layout "$layout" --version "$v" --platform "$p" --config "$config"
        if [ "$p" != "$host" ]; then
            exe="$executables/$(echo "$p" | tr '/' '-')/$exe_name"
            [ -n "$executables" ] && [ -f "$exe" ] || {
                echo "zip.sh: $a for $p needs the native executable at $exe (pass --executables <dir>)" >&2
                exit 2
            }
            set -- "$@" --executable "$exe"
        fi
        [ -z "$runtime_dir" ] || set -- "$@" --runtime-dir "$runtime_dir"
        # A platform the matrix has not enabled is built only on request. The
        # sandbox stand-in guest image of a local check is never used here.
        if [ -n "$local_check" ]; then
            [ "$a" != sandbox ] || [ -n "${SANDBOX_GUEST_IMAGE-}" ] || { echo "zip.sh: sandbox needs SANDBOX_GUEST_IMAGE" >&2; exit 2; }
            set -- "$@" --local-check
        fi
        echo "zip.sh: $a $v $p" >&2
        sh scripts/addons/package.sh "$@"
        "$ADDONPKG" zip --config "$config" --layout "$layout" --platform "$p" --version "$v" --out "$out"
        rm -rf "$layout"
        built=$((built + 1))
    done
done
[ "$built" -gt 0 ] || { echo "zip.sh: nothing was built" >&2; exit 1; }
echo "zip.sh: wrote $built zip file(s) to $out" >&2
