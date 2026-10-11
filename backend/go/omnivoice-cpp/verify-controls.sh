#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
cd "$root"
if [[ ! -f pkg/grpc/proto/backend.pb.go || ! -f pkg/grpc/proto/backend_grpc.pb.go ]]; then
  echo 'Missing generated protobuf Go files. Generate backend/backend.proto into pkg/grpc/proto with installed protoc and Go plugins before running this offline check.' >&2
  exit 1
fi
if [[ $(uname -s) != Linux ]]; then
  echo 'This model-free native verification currently requires Linux.' >&2
  exit 1
fi
build=$(mktemp -d "$root/.omnivoice-controls.XXXXXX")
trap 'rm -rf "$build"' EXIT
backend="$root/backend/go/omnivoice-cpp"
# Ordinary package tests must not depend on the optional native fixture.
env -u OMNIVOICE_TEST_LIBRARY go test -mod=readonly -count=1 -timeout=60s ./backend/go/omnivoice-cpp -ginkgo.label-filter='!e2e' -ginkgo.fail-on-empty
# Selecting native specs without their fixture must fail, not silently skip.
if env -u OMNIVOICE_TEST_LIBRARY go test -tags=native_controls -mod=readonly -count=1 -timeout=60s ./backend/go/omnivoice-cpp -ginkgo.label-filter='native-controls' -ginkgo.fail-on-empty -ginkgo.no-color >"$build/missing-fixture.log" 2>&1; then
  cat "$build/missing-fixture.log"
  echo 'Native controls unexpectedly passed without their fixture.' >&2
  exit 1
fi
cat "$build/missing-fixture.log"
# Do not mistake compilation errors or an empty selection for the expected failure.
grep -Fq 'run verify-controls.sh to compile the actual wrapper fixture' "$build/missing-fixture.log"
"${CXX:-c++}" -std=c++17 -shared -fPIC -I"$backend/testdata/native" \
  "$backend/cpp/gomnivoicecpp.cpp" "$backend/testdata/native/engine.cpp" \
  -o "$build/libomnivoice-controls.so"
export OMNIVOICE_TEST_LIBRARY="$build/libomnivoice-controls.so"
go test -tags=native_controls -mod=readonly -count=1 -timeout=60s ./backend/go/omnivoice-cpp -ginkgo.label-filter='native-controls' -ginkgo.fail-on-empty
go test -tags=native_controls -mod=readonly -count=1 -timeout=60s ./backend/go/omnivoice-cpp -ginkgo.label-filter='!e2e'
git diff --check
