#!/bin/sh
# SPDX-License-Identifier: MIT
# Builds the first-party add-on OCI package of one platform (the add-on is
# named by the platform file, Tables by default) as a local OCI
# layout. It never pushes, tags, signs or logs in to anything.
#
#   sh scripts/addons/package.sh --layout <dir> --version <semver> --platform <os/arch>
#       [--executable <file>] [--runtime-dir <dir>] [--revision <sha>]
#       [--config <platforms.yaml>] [--local-check] [--root <dir>]
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
#
# --root <dir> builds an add-on that lives outside this repository, in its own
# Go module. The layout is <dir>/package/platforms.yaml (the default --config),
# the manifest, ui and skills paths of that file are relative to <dir>, and the
# executable is built from <dir>/cmd/<executable name> with the go.mod of
# <dir>, unless --executable is given. Every path flag (--layout, --executable,
# --runtime-dir, --config, --root) is relative to the directory the script was
# started in. A relative <dir> is relative to the
# directory the script was started in. An add-on outside this repository
# cannot have a UI yet: the UI build reads the UI tree of this repository.
#
# The Sandbox add-on needs the reference of its guest image, built into the
# executable with -X main.guestImage. Set SANDBOX_GUEST_IMAGE to
# <repository>@sha256:<digest> (the output of the sandbox-images workflow).
# The media profile has its own image: SANDBOX_GUEST_IMAGE_MEDIA,
# <repository>@sha256:<digest>, optional. It reaches the executable as the
# flag -X main.profileImages=media=<ref>; a profile without an image is not
# offered by that release.
# Without SANDBOX_GUEST_IMAGE the script refuses to build or accept a Sandbox executable, unless
# --local-check is given: the build then uses a stand-in reference that no
# registry serves, and the receipt is not publishable anyway.
set -eu
start=$PWD
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
addon_root=
layout= version= platform= executable= runtime_dir= revision= local_check=
config=
while [ "$#" -gt 0 ]; do
    case "$1" in
    --layout) layout=${2-}; shift 2 ;;
    --version) version=${2-}; shift 2 ;;
    --platform) platform=${2-}; shift 2 ;;
    --executable) executable=${2-}; shift 2 ;;
    --runtime-dir) runtime_dir=${2-}; shift 2 ;;
    --revision) revision=${2-}; shift 2 ;;
    --config) config=${2-}; shift 2 ;;
    --root) addon_root=${2-}; shift 2 ;;
    --local-check) local_check=1; shift ;;
    *) echo "package.sh: unknown argument $1" >&2; exit 2 ;;
    esac
done
[ -n "$layout" ] && [ -n "$version" ] && [ -n "$platform" ] || {
    echo 'usage: package.sh --layout <dir> --version <version> --platform <os/arch>' >&2
    exit 2
}
# A relative path flag is relative to the caller's directory, not the checkout.
absp() { case "$1" in '') ;; /*) printf '%s\n' "$1" ;; *) printf '%s/%s\n' "$start" "$1" ;; esac; }
layout=$(absp "$layout"); executable=$(absp "$executable"); runtime_dir=$(absp "$runtime_dir"); config=$(absp "$config")
if [ -n "$addon_root" ]; then
    case "$addon_root" in /*) ;; *) addon_root=$start/$addon_root ;; esac
    addon_root=$(CDPATH= cd -- "$addon_root" 2>/dev/null && pwd) || { echo "package.sh: --root is not a directory" >&2; exit 2; }
    [ -n "$config" ] || config=$addon_root/package/platforms.yaml
fi
[ -n "$config" ] || config=addons/tables/package/platforms.yaml
scratch=$(mktemp -d "${TMPDIR:-/tmp}/addon-package.XXXXXX")
trap 'chmod -R u+rwx "$scratch" 2>/dev/null; rm -rf "$scratch"' EXIT HUP INT TERM
tool=${ADDONPKG:-}
if [ -z "$tool" ]; then
    go build -o "$scratch/addonpkg" ./scripts/addons/cmd/addonpkg
    tool="$scratch/addonpkg"
fi
# The add-on, its executable and its UI directory come from the platform file.
[ -f "$config" ] || { echo "package.sh: platform file $config not found" >&2; exit 2; }
grep -q '^manifest: ' "$config" || { echo "package.sh: platform file $config has no manifest key" >&2; exit 2; }
addon=$(sed -n 's/^addon: //p' "$config")
exe_name=$(sed -n 's/^executable: //p' "$config")
ui_dir=$(sed -n 's/^ui: //p' "$config")
for v in "$addon" "$exe_name"; do
    case "$v" in
    '' | *[!a-z0-9-]*) echo "package.sh: $config needs a plain addon and executable name" >&2; exit 2 ;;
    esac
done
ldflags=
if [ "$addon" = sandbox ] && [ -z "$addon_root" ]; then
    guest=${SANDBOX_GUEST_IMAGE-}
    if [ -z "$guest" ]; then
        [ -n "$local_check" ] || {
            echo 'package.sh: the sandbox add-on needs SANDBOX_GUEST_IMAGE=<repository>@sha256:<digest> (see .github/workflows/sandbox-images.yml)' >&2
            exit 2
        }
        guest=localcheck.invalid/sandbox-guest@sha256:0000000000000000000000000000000000000000000000000000000000000000
    fi
    printf '%s\n' "$guest" | grep -E '^[A-Za-z0-9._/:-]+@sha256:[0-9a-f]{64}$' >/dev/null || {
        echo 'package.sh: SANDBOX_GUEST_IMAGE must be <repository>@sha256:<64 hex digits>' >&2
        exit 2
    }
    ldflags="-X main.guestImage=$guest"
    extra=
    for pair in media:${SANDBOX_GUEST_IMAGE_MEDIA-}; do
        ref=${pair#*:}
        [ -n "$ref" ] || continue
        printf '%s\n' "$ref" | grep -E '^[A-Za-z0-9._/:-]+@sha256:[0-9a-f]{64}$' >/dev/null || {
            echo "package.sh: SANDBOX_GUEST_IMAGE_$(printf '%s' "${pair%%:*}" | tr a-z A-Z) must be <repository>@sha256:<64 hex digits>" >&2
            exit 2
        }
        extra="${extra:+$extra,}${pair%%:*}=$ref"
    done
    [ -z "$extra" ] || ldflags="$ldflags -X main.profileImages=$extra"
fi
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
    if [ -n "$addon_root" ]; then
        # Pure Go unless the caller sets CGO_ENABLED.
        cgo=${CGO_ENABLED:-0}
        [ -d "$addon_root/cmd/$exe_name" ] || {
            echo "package.sh: no $addon_root/cmd/$exe_name to build; pass --executable" >&2
            exit 2
        }
        (cd "$addon_root" && CGO_ENABLED=$cgo go build -trimpath -o "$scratch/$exe_name" "./cmd/$exe_name")
    elif [ -n "$ldflags" ]; then
        CGO_ENABLED=$cgo go build -trimpath -ldflags "$ldflags" -o "$scratch/$exe_name" "./addons/$addon/cmd/$exe_name"
    else
        CGO_ENABLED=$cgo go build -trimpath -o "$scratch/$exe_name" "./addons/$addon/cmd/$exe_name"
    fi
    executable="$scratch/$exe_name"
elif [ -n "$ldflags" ] && [ -z "$local_check" ]; then
    # A binary built elsewhere must carry the guest image too. Its build info
    # cannot show it (with -trimpath, Go leaves -ldflags out), so look for
    # the reference in the executable.
    grep -aqF -- "$guest" "$executable" || {
        echo "package.sh: $executable was not built with -X main.guestImage" >&2
        exit 2
    }
    for ref in $(printf '%s' "${extra-}" | tr ',' ' '); do
        grep -aqF -- "${ref#*=}" "$executable" || {
            echo "package.sh: $executable was not built with -X main.profileImages ($ref)" >&2
            exit 2
        }
    done
fi
# The UI is a build output. Build it only when it is absent. An add-on in
# another repository brings its own UI build output (or none).
if [ -n "$addon_root" ]; then
    :
elif [ -n "$ui_dir" ] && [ ! -f "$ui_dir/ui/$addon.js" ]; then
    node "addons/$addon/ui/build.mjs"
fi
set -- --config "$config" --layout "$layout" --version "$version" --platform "$platform" --executable "$executable"
[ -z "$runtime_dir" ] || set -- "$@" --runtime-dir "$runtime_dir"
[ -z "$revision" ] || set -- "$@" --revision "$revision"
[ -z "$local_check" ] || set -- "$@" --local-check
[ -z "$addon_root" ] || set -- "$@" --root "$addon_root"
"$tool" build "$@"
"$tool" verify --config "$config" --layout "$layout" --platform "$platform" --version "$version"
