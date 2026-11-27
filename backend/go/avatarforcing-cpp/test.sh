#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")
cd "$CURDIR"

# The unit specs always run. The end-to-end spec needs libavatarforcing.so
# and AVATARFORCING_TEST_GGUF, AVATARFORCING_TEST_PORTRAIT and
# AVATARFORCING_TEST_AUDIO (see avatarforcing_test.go); it is skipped
# without them.
LD_LIBRARY_PATH="$CURDIR:${LD_LIBRARY_PATH:-}" go test -v -count=1 -timeout 1800s .
