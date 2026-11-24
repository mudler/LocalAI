#!/bin/bash
#
# Assemble the backend image contents in package/:
#   strata          the Go backend (pure Go, no shared libraries)
#   run.sh          entry point; points the backend at everything below
#   strata-engine   launcher for the Strata engine (picks engines/<name>)
#   engines/<name>/ upstream engine package(s), each with its own CUDA / ROCm /
#                   oneAPI / Metal runtime and RUNPATH $ORIGIN
#   engines-hy4/<name>/
#                   the same targets for Tencent Hy4-Flash, whose engine is
#                   the separate program strata-hy4
#   engines-inkling/<name>/
#                   the same targets for Thinking Machines Inkling 2 Mini,
#                   whose engine is the separate program strata-inkling
#   python/         portable CPython with regex and jinja2
#   strata-src/     the Strata Python modules the tokenizer helper imports
#
# Everything under engines/, python/ and strata-src/ comes from `make engine`.
# No GPU runtime is added from the build image: the engine packages already
# carry theirs, and a second copy would only add size and risk a mismatch.

set -e

CURDIR=$(dirname "$(realpath "$0")")

for d in engines engines-hy4 engines-inkling python strata-src; do
	if [ ! -d "$CURDIR/$d" ]; then
		echo "ERROR: $CURDIR/$d is missing, run 'make engine' first" >&2
		exit 1
	fi
done
if [ ! -x "$CURDIR/strata" ]; then
	echo "ERROR: $CURDIR/strata is missing, run 'make strata' first" >&2
	exit 1
fi
for d in engines engines-hy4 engines-inkling; do
	if [ -z "$(ls -A "$CURDIR/$d")" ]; then
		echo "ERROR: $CURDIR/$d is empty" >&2
		exit 1
	fi
done

rm -rf "$CURDIR/package"
mkdir -p "$CURDIR/package"

cp -avf "$CURDIR/strata" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/strata-engine" "$CURDIR/package/"
chmod +x "$CURDIR/package/run.sh" "$CURDIR/package/strata-engine"

# cp -a keeps the symlinks that the CUDA / ROCm library packages use.
cp -af "$CURDIR/engines" "$CURDIR/package/engines"
cp -af "$CURDIR/engines-hy4" "$CURDIR/package/engines-hy4"
cp -af "$CURDIR/engines-inkling" "$CURDIR/package/engines-inkling"
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
for e in "$CURDIR"/package/engines-hy4/*/; do
	if [ ! -x "${e}strata-hy4" ]; then
		echo "ERROR: ${e}strata-hy4 is not executable" >&2
		exit 1
	fi
done
for e in "$CURDIR"/package/engines-inkling/*/; do
	if [ ! -x "${e}strata-inkling" ]; then
		echo "ERROR: ${e}strata-inkling is not executable" >&2
		exit 1
	fi
done

echo "Packaging completed successfully"
ls -liah "$CURDIR/package/" "$CURDIR/package/engines/" "$CURDIR/package/engines-hy4/" "$CURDIR/package/engines-inkling/"
