#!/bin/sh
# Fetch one Strata engine package, check its SHA-256 and unpack it.
# Usage: fetch-engine.sh <name> <asset file> <sha256> <release url> <stamp>
# Unpacks into engines/<name>/. Does nothing when that directory already holds
# this version (the stamp file).
set -eu

name=${1:?name}
file=${2:?asset file}
sha=${3:?sha256}
base=${4:?release url}
stamp=${5:?stamp}

dest="engines/$name"
if [ -f "$dest/.stamp-$stamp" ]; then
	echo "==> $dest is up to date"
	exit 0
fi

rm -rf "$dest"
mkdir -p "$dest" downloads
echo "==> fetching $file"
sh ./fetch-asset.sh "$base/$file" "$sha" "downloads/$file"
tar -xzf "downloads/$file" -C "$dest"
rm -f "downloads/$file"
if [ ! -x "$dest/strata" ]; then
	echo "strata: $file has no executable 'strata' at its top level" >&2
	exit 1
fi
touch "$dest/.stamp-$stamp"
