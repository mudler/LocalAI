#!/bin/sh
# SPDX-License-Identifier: MIT
# Builds the first-party add-on OCI package of one platform (the add-on is
# named by the platform file, Tables by default) as a local OCI
# layout. It never pushes, tags, signs or logs in to anything.
#
#   sh scripts/addons/package.sh --layout <dir> --version <semver> --platform <os/arch>
#       [--executable <file>] [--runtime-dir <dir>] [--revision <sha>]
#       [--config <platforms.yaml>] [--local-check]
#
# The layout holds one platform image: an executable layer (the entrypoint and
# the runtime files of the platform), a ui layer when the manifest declares a
# UI, and the manifest layer, plus receipt.json with the digests. The layout is
# verified before the script succeeds.
#
# --executable is the built binary of the platform. Without it the script
# builds the executable of the add-on for the platform of this host and refuses any
# other platform: a package is never made from a binary of another platform.
# --local-check allows a platform that the platform matrix has not enabled yet;
# the receipt then says publishable=false and the publish steps refuse it.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
layout= version= platform= executable= runtime_dir= revision= local_check=
config=addons/tables/package/platforms.yaml
while [ "$#" -gt 0 ]; do
    case "$1" in
    --layout) layout=${2-}; shift 2 ;;
    --version) version=${2-}; shift 2 ;;
    --platform) platform=${2-}; shift 2 ;;
    --executable) executable=${2-}; shift 2 ;;
    --runtime-dir) runtime_dir=${2-}; shift 2 ;;
    --revision) revision=${2-}; shift 2 ;;
    --config) config=${2-}; shift 2 ;;
    --local-check) local_check=1; shift ;;
    *) echo "package.sh: unknown argument $1" >&2; exit 2 ;;
    esac
done
[ -n "$layout" ] && [ -n "$version" ] && [ -n "$platform" ] || {
    echo 'usage: package.sh --layout <dir> --version <version> --platform <os/arch>' >&2
    exit 2
}
scratch=$(mktemp -d "${TMPDIR:-/tmp}/addon-package.XXXXXX")
trap 'chmod -R u+rwx "$scratch" 2>/dev/null; rm -rf "$scratch"' EXIT HUP INT TERM
tool=${ADDONPKG:-}
if [ -z "$tool" ]; then
    go build -o "$scratch/addonpkg" ./scripts/addons/cmd/addonpkg
    tool="$scratch/addonpkg"
fi
# The add-on, its executable and its UI directory come from the platform file.
[ -f "$config" ] || { echo "package.sh: platform file $config not found" >&2; exit 2; }
addon=$(sed -n 's/^addon: //p' "$config")
exe_name=$(sed -n 's/^executable: //p' "$config")
ui_dir=$(sed -n 's/^ui: //p' "$config")
for v in "$addon" "$exe_name"; do
    case "$v" in
    '' | *[!a-z0-9-]*) echo "package.sh: $config needs a plain addon and executable name" >&2; exit 2 ;;
    esac
done
if [ -z "$executable" ]; then
    host="$(go env GOOS)/$(go env GOARCH)"
    [ "$platform" = "$host" ] || {
        echo "package.sh: platform $platform is not this host ($host); pass the native --executable" >&2
        exit 2
    }
    # Tables links a C library. The other add-ons are pure Go, so their
    # executable needs no C toolchain on any platform.
    cgo=0
    [ "$addon" != tables ] || cgo=1
    CGO_ENABLED=$cgo go build -trimpath -o "$scratch/$exe_name" "./addons/$addon/cmd/$exe_name"
    executable="$scratch/$exe_name"
fi
# The UI is a build output. Build it only when it is absent.
if [ -n "$ui_dir" ] && [ ! -f "$ui_dir/ui/$addon.js" ]; then
    node "addons/$addon/ui/build.mjs"
fi
set -- --config "$config" --layout "$layout" --version "$version" --platform "$platform" --executable "$executable"
[ -z "$runtime_dir" ] || set -- "$@" --runtime-dir "$runtime_dir"
[ -z "$revision" ] || set -- "$@" --revision "$revision"
[ -z "$local_check" ] || set -- "$@" --local-check
"$tool" build "$@"
"$tool" verify --config "$config" --layout "$layout" --platform "$platform" --version "$version"
