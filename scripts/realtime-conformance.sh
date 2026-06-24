#!/bin/sh
# realtime-conformance: verify the realtime state-machine implementations conform
# to their formal designs. See docs/design/realtime-state-machines.md (Part 6).
#
# Two layers, BOTH required by default -- this gate is FAIL-CLOSED:
#   1. Go-native conformance: the respcoord + turncoord transition tables +
#      Ginkgo/Gomega seeded property tests under the race detector (checks the
#      implementation).
#   2. FizzBee model check of the authoritative .fizz specs (checks the design,
#      and is the thing that makes the design authoritative).
#
# A missing FizzBee is a HARD FAILURE, not a skip -- otherwise verification
# silently evaporates the moment the tool is inconvenient, which defeats the
# whole point. The pinned binary installs reproducibly via scripts/install-fizzbee.sh
# (checksums in formal-verification/fizzbee.sha256), so "couldn't install" is not a
# reason to skip; fix the install. The ONLY way to skip is the explicit, loud
# REALTIME_CONFORMANCE_SKIP_FIZZBEE=1 opt-out, for the rare case of hacking on
# unrelated code locally -- never in CI.
#
# POSIX sh (no bashisms): the project requires tooling scripts to be portable.
set -eu

ROOT=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
GOCMD=${GOCMD:-go}
SPEC_DIR="$ROOT/formal-verification"

echo "==> [1/2] Go conformance (coordinator, respcoord, turncoord, conncoord, compactcoord, ttscoord) with -race"
"$GOCMD" test -race -count=1 \
    "$ROOT/core/http/endpoints/openai/coordinator/..." \
    "$ROOT/core/http/endpoints/openai/respcoord/..." \
    "$ROOT/core/http/endpoints/openai/turncoord/..." \
    "$ROOT/core/http/endpoints/openai/conncoord/..." \
    "$ROOT/core/http/endpoints/openai/compactcoord/..." \
    "$ROOT/core/http/endpoints/openai/ttscoord/..."

# Locate the FizzBee CLI wrapper. install-fizzbee.sh publishes a stable symlink
# at .tools/fizzbee/fizz; otherwise fall back to `fizz` on PATH. FIZZBEE_BIN
# overrides both. NOTE: this must be the `fizz` WRAPPER (which runs the bundled
# parser then the checker), not the raw `fizzbee` binary, and its sibling
# parser/ + fizz.env must be intact.
FIZZBEE_BIN=${FIZZBEE_BIN:-}
if [ -z "$FIZZBEE_BIN" ]; then
    if [ -x "$ROOT/.tools/fizzbee/fizz" ]; then
        FIZZBEE_BIN="$ROOT/.tools/fizzbee/fizz"
    elif command -v fizz >/dev/null 2>&1; then
        FIZZBEE_BIN=fizz
    fi
fi

echo "==> [2/2] FizzBee model check of authoritative specs"
if [ -n "$FIZZBEE_BIN" ] && { [ -x "$FIZZBEE_BIN" ] || command -v "$FIZZBEE_BIN" >/dev/null 2>&1; }; then
    for spec in "$SPEC_DIR"/*.fizz; do
        echo "    checking $spec"
        # CLI is `fizz [flags] <spec.fizz>` (default = exhaustive BFS); there is
        # NO `run` subcommand. The checker may print FAILED/DEADLOCK but still
        # exit 0, so detect violations from the output as well as the exit code.
        set +e
        out=$("$FIZZBEE_BIN" "$spec" 2>&1)
        rc=$?
        set -e
        printf '%s\n' "$out"
        if [ "$rc" -ne 0 ]; then
            echo "ERROR: FizzBee exited $rc on $spec" >&2
            exit 1
        fi
        if printf '%s\n' "$out" | grep -qE '^(FAILED|DEADLOCK)'; then
            echo "ERROR: FizzBee reported an invariant/deadlock violation in $spec" >&2
            exit 1
        fi
    done
    echo "==> realtime-conformance OK (Go + FizzBee)"
elif [ "${REALTIME_CONFORMANCE_SKIP_FIZZBEE:-0}" = "1" ]; then
    echo "    !! FizzBee model check EXPLICITLY SKIPPED (REALTIME_CONFORMANCE_SKIP_FIZZBEE=1)" >&2
    echo "    !! The authoritative design was NOT verified this run. Do not use in CI." >&2
    echo "==> realtime-conformance INCOMPLETE (Go only; design check skipped by request)"
else
    echo "ERROR: FizzBee not found -- the authoritative design cannot be verified." >&2
    echo "       This gate is fail-closed; verification is not optional." >&2
    echo "       Install the pinned, checksum-verified binary:  make install-fizzbee" >&2
    echo "       (see formal-verification/README.md). To deliberately skip locally while" >&2
    echo "       hacking on unrelated code, set REALTIME_CONFORMANCE_SKIP_FIZZBEE=1." >&2
    exit 1
fi
