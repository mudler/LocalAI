#!/bin/sh
# SPDX-License-Identifier: MIT
# Prints what one workflow event may publish, as key=value lines:
#   publish, reason, immutable, aliases, channel, signer_ref, version
#
#   sh scripts/addons/channel.sh --event <name> --ref <full ref> --sha <40 hex>
#       [--draft] [--prerelease]
#
# Pull requests and branch pushes never publish, and master alone publishes
# development. A tag push prepares the immutable version and moves no alias.
# A published stable release moves latest.
# Anything the rules do not name fails.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
if [ -n "${ADDONPKG:-}" ]; then
    exec "$ADDONPKG" channel "$@"
fi
exec go run ./scripts/addons/cmd/addonpkg channel "$@"
