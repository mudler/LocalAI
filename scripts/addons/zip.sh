#!/bin/sh
# SPDX-License-Identifier: MIT
# Writes the add-on zip files that "Install from a file" takes: one zip and one
# .sha256 file per add-on and platform. It is not a second build path: every zip
# is made from the verified OCI layout that package.sh builds, so it holds
# the same executable, UI, icon, skills and manifest as the registry package.
#
#   sh scripts/addons/zip.sh [--addon <id>] [--platforms <os/arch,...>]
#       [--out <dir>] [--version <semver>] [--executables <dir>]
#       [--runtime-dir <dir>] [--local-check] [--root <dir>] [--executable <file>]
#
# Without --addon every add-on that has a platform file is built, except the
# Sandbox add-on when SANDBOX_GUEST_IMAGE is not set (its executable needs the
# reference of its guest image; see package.sh). Without --platforms the
# platform of this host is built. A platform other than this host needs the
# native executable: put it at <executables>/<os>-<arch>/<executable name>.
# --local-check allows a platform that the platform matrix has not enabled.
# --version defaults to the version in the manifest of the add-on.
#
# --root <dir> builds the zip of an add-on that lives outside this repository
# (see package.sh): <dir>/package/platforms.yaml, the executable from
# <dir>/cmd/<executable name>. --addon is not needed then. --out defaults to
# <dir>/dist/addons. --executable <file> is the built binary to pack instead of
# building one; use it with a single platform. Set ADDONPKG to the path of an
# installed addonpkg to skip building it from this checkout.
#
# The files are written to --out, dist/addons by default:
#   <id>-<version>-<os>-<arch>.zip
#   <id>-<version>-<os>-<arch>.zip.sha256   (sha256sum format)
# Install one with:  local-ai addons install dist/addons/<name>.zip
set -eu
start=$PWD
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
addon= platforms= out= version= executables= runtime_dir= local_check= addon_root= executable=
while [ "$#" -gt 0 ]; do
    case "$1" in
    --addon) addon=${2-}; shift 2 ;;
    --platforms) platforms=${2-}; shift 2 ;;
    --out) out=${2-}; shift 2 ;;
    --version) version=${2-}; shift 2 ;;
    --executables) executables=${2-}; shift 2 ;;
    --runtime-dir) runtime_dir=${2-}; shift 2 ;;
    --local-check) local_check=1; shift ;;
    --root) addon_root=${2-}; shift 2 ;;
    --executable) executable=${2-}; shift 2 ;;
    *) echo "zip.sh: unknown argument $1" >&2; exit 2 ;;
    esac
done
if [ -n "$addon_root" ]; then
    case "$addon_root" in /*) ;; *) addon_root=$start/$addon_root ;; esac
    addon_root=$(CDPATH= cd -- "$addon_root" 2>/dev/null && pwd) || { echo "zip.sh: --root is not a directory" >&2; exit 2; }
    [ -f "$addon_root/package/platforms.yaml" ] || { echo "zip.sh: $addon_root/package/platforms.yaml not found" >&2; exit 2; }
    [ -n "$out" ] || out=$addon_root/dist/addons
fi
[ -n "$out" ] || out=dist/addons
host="$(go env GOOS)/$(go env GOARCH)"
[ -n "$platforms" ] || platforms=$host
# Work files live under the build cache of the checkout, never in /tmp.
work_root=${ADDON_ZIP_WORK:-$root/.cache/addons/zip}
mkdir -p "$work_root" "$out"
work=$(mktemp -d "$work_root/run.XXXXXX")
trap 'chmod -R u+rwx "$work" 2>/dev/null; rm -rf "$work"' EXIT HUP INT TERM
if [ -z "${ADDONPKG-}" ]; then
    go build -o "$work/addonpkg" ./scripts/addons/cmd/addonpkg
    ADDONPKG="$work/addonpkg"
fi
export ADDONPKG

if [ -n "$addon_root" ]; then
    list=$(sed -n 's/^addon: //p' "$addon_root/package/platforms.yaml")
    [ -z "$addon" ] || [ "$addon" = "$list" ] || { echo "zip.sh: --addon $addon does not match the add-on of --root ($list)" >&2; exit 2; }
elif [ -n "$addon" ]; then
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
    manifest="addons/$a/manifest.json"
    if [ -n "$addon_root" ]; then
        config=$addon_root/package/platforms.yaml
        manifest=$addon_root/$(sed -n 's/^manifest: //p' "$config")
    fi
    exe_name=$(sed -n 's/^executable: //p' "$config")
    v=$version
    [ -n "$v" ] || v=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$manifest" | head -1)
    [ -n "$v" ] || { echo "zip.sh: no version for $a; pass --version" >&2; exit 2; }
    for p in $(echo "$platforms" | tr ',' ' '); do
        layout="$work/$a-$(echo "$p" | tr '/' '-')"
        set -- --layout "$layout" --version "$v" --platform "$p" --config "$config"
        if [ -n "$executable" ]; then
            set -- "$@" --executable "$executable"
        elif [ "$p" != "$host" ]; then
            exe="$executables/$(echo "$p" | tr '/' '-')/$exe_name"
            [ -n "$executables" ] && [ -f "$exe" ] || {
                echo "zip.sh: $a for $p needs the native executable at $exe (pass --executables <dir>)" >&2
                exit 2
            }
            set -- "$@" --executable "$exe"
        fi
        [ -z "$runtime_dir" ] || set -- "$@" --runtime-dir "$runtime_dir"
        [ -z "$addon_root" ] || set -- "$@" --root "$addon_root"
        # A platform the matrix has not enabled is built only on request. The
        # sandbox stand-in guest image of a local check is never used here.
        if [ -n "$local_check" ]; then
            [ -n "$addon_root" ] || [ "$a" != sandbox ] || [ -n "${SANDBOX_GUEST_IMAGE-}" ] || { echo "zip.sh: sandbox needs SANDBOX_GUEST_IMAGE" >&2; exit 2; }
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
