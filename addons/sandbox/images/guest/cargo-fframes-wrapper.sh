#!/bin/sh
# Runs the cargo-fframes CLI. After "cargo fframes new <dir>" it pins
# fframes-media in the new project to FFRAMES_MEDIA_VERSION: the template pins
# fframes only, and cargo would otherwise take a newer fframes-media that does
# not compile with it.
set -eu
real=/usr/local/libexec/fframes/bin/cargo-fframes
"$real" "$@"
[ "${1:-}" = fframes ] && shift
[ "${1:-}" = new ] || exit 0
for dir in "$@"; do :; done
manifest=$dir/Cargo.toml
[ -f "$manifest" ] && [ -n "${FFRAMES_MEDIA_VERSION:-}" ] || exit 0
grep -q '^fframes-media' "$manifest" && exit 0
sed -i "0,/^\[dependencies\]/s//[dependencies]\nfframes-media = \"=${FFRAMES_MEDIA_VERSION}\"/" "$manifest"
