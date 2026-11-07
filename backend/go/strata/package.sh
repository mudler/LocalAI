#!/bin/bash
#
# Assemble the backend image contents in package/:
#   strata          the Go backend (pure Go, no shared libraries)
#   run.sh          entry point; points the backend at everything below
#   strata-engine   launcher for the Strata engine (picks engines/<name>)
#   strata-dsv41-engine
#                   the same for the DeepSeek V4.1 engine, over engines-dsv41/<name>/
#                   (present only for the targets Strata ships a package for)
#   engines/<name>/ upstream engine package(s), each with its own CUDA / ROCm /
#                   oneAPI / Metal runtime and RUNPATH $ORIGIN
#   python/         portable CPython with regex and jinja2
#   strata-src/     the Strata Python modules the tokenizer helper imports
#
# Everything under engines/, python/ and strata-src/ comes from `make engine`.
# No GPU runtime is added from the build image: the engine packages already
# carry theirs, and a second copy would only add size and risk a mismatch.

set -e

CURDIR=$(dirname "$(realpath "$0")")

for d in engines python strata-src; do
	if [ ! -d "$CURDIR/$d" ]; then
		echo "ERROR: $CURDIR/$d is missing, run 'make engine' first" >&2
		exit 1
	fi
done
if [ ! -x "$CURDIR/strata" ]; then
	echo "ERROR: $CURDIR/strata is missing, run 'make strata' first" >&2
	exit 1
fi
if [ -z "$(ls -A "$CURDIR/engines")" ]; then
	echo "ERROR: $CURDIR/engines is empty" >&2
	exit 1
fi

rm -rf "$CURDIR/package"
mkdir -p "$CURDIR/package"

cp -avf "$CURDIR/strata" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/strata-engine" "$CURDIR/strata-dsv41-engine" "$CURDIR/package/"
chmod +x "$CURDIR/package/run.sh" "$CURDIR/package/strata-engine" "$CURDIR/package/strata-dsv41-engine"

# cp -a keeps the symlinks that the CUDA / ROCm library packages use.
cp -af "$CURDIR/engines" "$CURDIR/package/engines"
# Only the targets Strata ships a DeepSeek engine for have this folder.
if [ -d "$CURDIR/engines-dsv41" ] && [ -n "$(ls -A "$CURDIR/engines-dsv41")" ]; then
	cp -af "$CURDIR/engines-dsv41" "$CURDIR/package/engines-dsv41"
fi
cp -af "$CURDIR/python" "$CURDIR/package/python"
cp -af "$CURDIR/strata-src" "$CURDIR/package/strata-src"

# Drop the make stamps and caches; they are not needed at run time.
find "$CURDIR/package" -name '.stamp-*' -type f -delete
find "$CURDIR/package/python" -name '__pycache__' -type d -prune -exec rm -rf {} + 2>/dev/null || true

# Fail the build, not the first request, if an engine cannot be started.
for e in "$CURDIR"/package/engines/*/; do
	if [ ! -x "${e}strata" ]; then
		echo "ERROR: ${e}strata is not executable" >&2
		exit 1
	fi
done

if [ -d "$CURDIR/package/engines-dsv41" ]; then
	for e in "$CURDIR"/package/engines-dsv41/*/; do
		if [ ! -x "${e}strata-dsv41" ]; then
			echo "ERROR: ${e}strata-dsv41 is not executable" >&2
			exit 1
		fi
	done
fi

echo "Packaging completed successfully"
ls -liah "$CURDIR/package/" "$CURDIR/package/engines/"
