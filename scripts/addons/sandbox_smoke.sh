#!/bin/sh
# SPDX-License-Identifier: MIT
# Release smoke test of the Sandbox add-on package. It takes the package that
# the release process builds, starts the add-on from the extracted files under
# the real supervisor with the default settings, and runs a command through
# the same tool path a chat uses (tests/addons/actione2e, the "Packaged Sandbox smoke" spec).
#
#   sh scripts/addons/sandbox_smoke.sh [--layout <dir>] [--version <semver>]
#       [--runtime-dir <dir>] [--require isolated] [--local-check]
#
# --layout       a layout that package.sh made for this host. Without it the
#                script makes one: that needs --runtime-dir (the output of
#                addons/sandbox/package/fetch-runtime.sh) and, for a release
#                build, SANDBOX_GUEST_IMAGE (see package.sh).
# --require isolated
#                fail unless the host runs the command in gVisor or KVM.
#                Without it, a host that cannot run commands passes when every
#                tool answers "This host cannot run commands." and nothing runs.
# --local-check  allow a package with a stand-in guest image, for a check
#                that does not publish.
# Scratch files go under SANDBOX_SMOKE_WORK (default: .cache/sandbox-smoke).
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
layout= version=0.0.0-smoke runtime_dir= require= local_check=
while [ "$#" -gt 0 ]; do
    case "$1" in
    --layout) layout=${2-}; shift 2 ;;
    --version) version=${2-}; shift 2 ;;
    --runtime-dir) runtime_dir=${2-}; shift 2 ;;
    --require) require=${2-}; shift 2 ;;
    --local-check) local_check=1; shift ;;
    *) echo "sandbox_smoke.sh: unknown argument $1" >&2; exit 2 ;;
    esac
done
case "$require" in '' | isolated) ;; *) echo 'sandbox_smoke.sh: --require takes only "isolated"' >&2; exit 2 ;; esac
platform="$(go env GOOS)/$(go env GOARCH)"
case "$platform" in linux/*) ;; *) echo "sandbox_smoke.sh: the Sandbox add-on runs on Linux only (this host is $platform)" >&2; exit 2 ;; esac
config=addons/sandbox/package/platforms.yaml
work=${SANDBOX_SMOKE_WORK:-$root/.cache/sandbox-smoke}
rm -rf "$work"
mkdir -p "$work/tmp"
export TMPDIR="$work/tmp"
trap 'chmod -R u+rwx "$work" 2>/dev/null; rm -rf "$work"' EXIT HUP INT TERM
if [ -z "$layout" ]; then
    layout=$work/layout
    set -- --config "$config" --layout "$layout" --version "$version" --platform "$platform"
    [ -z "$runtime_dir" ] || set -- "$@" --runtime-dir "$runtime_dir"
    [ -z "$local_check" ] || set -- "$@" --local-check
    sh scripts/addons/package.sh "$@"
fi
dest=$work/package
mkdir -p "$dest"
go run ./scripts/addons/cmd/addonpkg extract --config "$config" --layout "$layout" --platform "$platform" --version "$version" --dest "$dest"
# A release package carries the guest image: without it neither isolated
# backend can start. The build info cannot show it: with -trimpath, Go leaves
# -ldflags out of it. So look for the reference in the executable itself: the
# one of SANDBOX_GUEST_IMAGE when it is set, any digest reference otherwise.
if [ -z "$local_check" ]; then
    if [ -n "${SANDBOX_GUEST_IMAGE-}" ]; then
        grep -aqF -- "$SANDBOX_GUEST_IMAGE" "$dest/sandbox"
    else
        grep -aqE -- '[A-Za-z0-9._/:-]+@sha256:[0-9a-f]{64}' "$dest/sandbox"
    fi || {
        echo 'sandbox_smoke.sh: the packaged executable has no guest image (-X main.guestImage)' >&2
        exit 1
    }
fi
SANDBOX_SMOKE_PACKAGE=$dest SANDBOX_SMOKE_REQUIRE=$require \
    go test -tags auth -count=1 -v -run TestActionE2E ./tests/addons/actione2e/ -ginkgo.focus 'Packaged Sandbox smoke' -ginkgo.fail-on-empty
