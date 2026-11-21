#!/bin/bash
# Checks that the run.sh of each llama.cpp family backend that ships an RPC
# server can be told which binary to run with LLAMACPP_BINARY.
#
# LocalAI workers use this to start the RPC server through run.sh, so it gets
# the same library path, GPU environment and bundled loader as the gRPC server.
# Three things must hold:
#
#   1. Without the variable, run.sh picks its binary as before.
#   2. With the variable, run.sh runs the named binary and passes the
#      arguments on.
#   3. The rest of the setup still runs when the override is used: the backend's
#      lib/ directory is first on LD_LIBRARY_PATH.
#
# The test builds a fake backend directory for each run.sh, with stand-in
# binaries that print their name, arguments and library path. There is no
# lib/ld.so, so run.sh starts the binary directly.
set -euo pipefail

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

REPO_ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")

RUN_SCRIPTS=(
    "backend/cpp/llama-cpp/run.sh llama-cpp"
    "backend/cpp/turboquant/run.sh turboquant"
    "backend/cpp/bonsai/run.sh bonsai"
)

failures=0

fail() {
    echo "FAIL: $*"
    failures=$((failures + 1))
}

make_program() {
    cat > "$1" <<'PROGRAM'
#!/bin/bash
echo "ran=$(basename "$0")"
echo "args=$*"
echo "libpath=${LD_LIBRARY_PATH:-}"
PROGRAM
    chmod +x "$1"
}

for entry in "${RUN_SCRIPTS[@]}"; do
    read -r script prefix <<< "$entry"
    run_sh="$REPO_ROOT/$script"

    if [ ! -f "$run_sh" ]; then
        fail "$script does not exist"
        continue
    fi

    dir="$WORK/$prefix"
    mkdir -p "$dir/lib"
    cp "$run_sh" "$dir/run.sh"
    chmod +x "$dir/run.sh"
    make_program "$dir/${prefix}-fallback"
    make_program "$dir/${prefix}-rpc-server"

    out=$(bash "$dir/run.sh" --port 1 2>/dev/null)
    if ! grep -qx "ran=${prefix}-fallback" <<< "$out"; then
        fail "$script: without LLAMACPP_BINARY expected ${prefix}-fallback, got: $out"
    fi

    out=$(LLAMACPP_BINARY="${prefix}-rpc-server" bash "$dir/run.sh" --host 0.0.0.0 --port 50052 2>/dev/null)
    if ! grep -qx "ran=${prefix}-rpc-server" <<< "$out"; then
        fail "$script: LLAMACPP_BINARY was not honored, got: $out"
    fi
    if ! grep -qx "args=--host 0.0.0.0 --port 50052" <<< "$out"; then
        fail "$script: arguments did not reach the overridden binary, got: $out"
    fi
    if ! grep -q "^libpath=$dir/lib" <<< "$out"; then
        fail "$script: lib/ is not first on LD_LIBRARY_PATH with the override, got: $out"
    fi

    # An empty value means no override.
    out=$(LLAMACPP_BINARY="" bash "$dir/run.sh" 2>/dev/null)
    if ! grep -qx "ran=${prefix}-fallback" <<< "$out"; then
        fail "$script: an empty LLAMACPP_BINARY changed the binary, got: $out"
    fi
done

if [ "$failures" -gt 0 ]; then
    echo "$failures check(s) failed"
    exit 1
fi

echo "PASS: every run.sh honors LLAMACPP_BINARY"
