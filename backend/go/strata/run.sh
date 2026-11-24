#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")

# The engine, the Strata Python modules and the Python that runs the tokenizer
# helper all ship inside this backend. The model options engine_path and
# strata_root still override them.
export STRATA_ENGINE_PATH="${STRATA_ENGINE_PATH:-$CURDIR/strata-engine}"
export STRATA_ROOT="${STRATA_ROOT:-$CURDIR/strata-src}"
export PATH="$CURDIR/python/bin:$PATH"

# The picture reader (strata-vision) ships inside the engine package that the launcher picks for this machine.
# Point the backend at it, unless the owner chose a reader or another engine. Without one the backend reports
# missing image support only when a request carries an image.
if [ -z "${STRATA_VISION_PATH:-}" ] && [ "$STRATA_ENGINE_PATH" = "$CURDIR/strata-engine" ] && [ -d "$CURDIR/engines" ]; then
	engine_dir=$("$CURDIR/strata-engine" --print-engine-dir 2>/dev/null) || engine_dir=""
	if [ -n "$engine_dir" ] && [ -x "$engine_dir/strata-vision" ]; then
		export STRATA_VISION_PATH="$engine_dir/strata-vision"
	fi
fi

exec "$CURDIR"/strata "$@"
