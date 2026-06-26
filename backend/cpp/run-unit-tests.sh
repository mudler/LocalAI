#!/bin/bash
#
# Discovers and runs every standalone C++ unit test under backend/cpp/.
#
# A "standalone" unit test is a *_test.cpp that depends only on the C++ standard
# library and nlohmann/json (single header) - i.e. it exercises pure helpers and
# does not need the full llama.cpp + gRPC backend build. Tests that DO need the
# backend build use the CMake/ctest path (e.g. -DLLAMA_GRPC_BUILD_TESTS=ON)
# instead and are skipped here.
#
# This keeps CI generic: adding a new pure-C++ unit test file named *_test.cpp in
# an active backend source dir is picked up automatically, with no CI edits.
#
# Env:
#   NLOHMANN_INCLUDE  include dir that contains nlohmann/json.hpp. If unset, the
#                     nlohmann/json single header is fetched to a temp dir.
#   CXX               compiler (default: g++).
#   JSON_VERSION      nlohmann/json tag to fetch when NLOHMANN_INCLUDE is unset
#                     (default: v3.11.3).
set -uo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
CXX="${CXX:-g++}"
JSON_VERSION="${JSON_VERSION:-v3.11.3}"

JSON_INC="${NLOHMANN_INCLUDE:-}"
if [ -z "$JSON_INC" ]; then
    JSON_INC="$(mktemp -d)"
    mkdir -p "$JSON_INC/nlohmann"
    echo "Fetching nlohmann/json ${JSON_VERSION} single header..."
    if ! curl -L -sf \
        "https://raw.githubusercontent.com/nlohmann/json/${JSON_VERSION}/single_include/nlohmann/json.hpp" \
        -o "$JSON_INC/nlohmann/json.hpp"; then
        echo "ERROR: failed to fetch nlohmann/json header" >&2
        exit 1
    fi
fi

# Active source dirs only - exclude per-variant build copies, dev snapshots and
# the vendored upstream llama.cpp tree.
mapfile -t tests < <(find "$ROOT" -name '*_test.cpp' \
    -not -path '*/llama.cpp/*' \
    -not -path '*-build/*' \
    -not -path '*-dev/*' \
    -not -path '*fallback*' | sort)

if [ "${#tests[@]}" -eq 0 ]; then
    echo "No standalone C++ unit tests found under $ROOT"
    exit 0
fi

fail=0
for test_src in "${tests[@]}"; do
    name="$(basename "$test_src" .cpp)"
    bin="$(mktemp -d)/$name"
    echo "==> $test_src"
    if ! "$CXX" -std=c++17 -Wall -Wextra \
        -I"$JSON_INC" -I"$(dirname "$test_src")" \
        "$test_src" -o "$bin"; then
        echo "COMPILE FAILED: $test_src" >&2
        fail=1
        continue
    fi
    if ! "$bin"; then
        echo "TEST FAILED: $test_src" >&2
        fail=1
    fi
done

echo "Ran ${#tests[@]} standalone C++ unit test file(s)"
exit "$fail"
