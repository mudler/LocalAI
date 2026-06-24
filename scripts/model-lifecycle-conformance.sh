#!/bin/sh
# model-lifecycle-conformance: check the model-loader shutdown behavior
# specified by formal-verification/model_loader_shutdown.fizz against the
# focused loader, gRPC client, distributed unloader, and worker tests.
#
# Both layers are required. The Go tests pin the concrete local/distributed
# paths; FizzBee exhaustively checks their progress and safety properties.
set -eu

ROOT=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
GOCMD=${GOCMD:-go}
SPEC="$ROOT/formal-verification/model_loader_shutdown.fizz"

cd "$ROOT"

echo "==> [1/3] Go lifecycle and busy-accounting conformance with -race"
"$GOCMD" test -race -count=1 ./pkg/model ./pkg/grpc ./core/services/worker

echo "==> [2/3] Go distributed force-propagation conformance with -race"
"$GOCMD" test -race -count=1 ./core/services/nodes -ginkgo.focus=RemoteUnloaderAdapter

# install-fizzbee.sh publishes a stable wrapper at .tools/fizzbee/fizz. The
# wrapper, rather than the raw fizzbee binary, runs both parser and checker.
FIZZBEE_BIN=${FIZZBEE_BIN:-}
if [ -z "$FIZZBEE_BIN" ]; then
    if [ -x "$ROOT/.tools/fizzbee/fizz" ]; then
        FIZZBEE_BIN="$ROOT/.tools/fizzbee/fizz"
    elif command -v fizz >/dev/null 2>&1; then
        FIZZBEE_BIN=fizz
    fi
fi

echo "==> [3/3] FizzBee model-loader lifecycle check"
if [ -z "$FIZZBEE_BIN" ] || { [ ! -x "$FIZZBEE_BIN" ] && ! command -v "$FIZZBEE_BIN" >/dev/null 2>&1; }; then
    echo "ERROR: FizzBee not found; lifecycle verification is fail-closed." >&2
    echo "       Install the pinned binary with: make install-fizzbee" >&2
    exit 1
fi

set +e
out=$("$FIZZBEE_BIN" "$SPEC" 2>&1)
rc=$?
set -e
printf '%s\n' "$out"

if [ "$rc" -ne 0 ]; then
    echo "ERROR: FizzBee exited $rc on $SPEC" >&2
    exit 1
fi
if printf '%s\n' "$out" | grep -qE '^(FAILED|DEADLOCK)'; then
    echo "ERROR: FizzBee reported a lifecycle property violation in $SPEC" >&2
    exit 1
fi

echo "==> model-lifecycle-conformance OK (Go + FizzBee)"
