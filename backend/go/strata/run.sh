#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")

# The engine, the Strata Python modules and the Python that runs the tokenizer
# helper all ship inside this backend. The model options engine_path and
# strata_root still override them.
export STRATA_ENGINE_PATH="${STRATA_ENGINE_PATH:-$CURDIR/strata-engine}"
export STRATA_DSV41_ENGINE_PATH="${STRATA_DSV41_ENGINE_PATH:-$CURDIR/strata-dsv41-engine}"
export STRATA_ROOT="${STRATA_ROOT:-$CURDIR/strata-src}"
export PATH="$CURDIR/python/bin:$PATH"

exec "$CURDIR"/strata "$@"
