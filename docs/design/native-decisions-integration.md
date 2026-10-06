# Native decisions integration smoke test

The opt-in Ginkgo spec in
`core/http/middleware/decisions_native_integration_test.go` exercises the actual
central classifier factory, `backend.NewDecisionRunner`, `ModelLoader`, and
llama.cpp's native decision pipeline over gRPC. It does not use an HTTP client,
fake classifier responses, or a mock inference backend. It checks independent
probabilities, overlapping labels, and selection of a candidate covering both
labels rather than the earlier single-label candidate. Candidate configuration
lookup is in-memory; the selected generation model is not loaded or invoked.

It also sends a native request through the Go runner and checks positive input
usage and zero generated output tokens. This is a request-contract test, not a
classification-quality benchmark: the overlap fixture intentionally uses a low
positive activation threshold (`0.000001`). Do not infer useful policy accuracy
from its success, or use this threshold as a deployment recommendation.

## Requirements

- Generate protobuf bindings with `make protogen-go`.
- Build the React UI normally, or, **only for Go tests in an isolated worktree**,
  satisfy the embed directive with a minimal fixture:

  ```sh
  mkdir -p core/http/react-ui/dist
  printf '<!doctype html><title>Test-only embed fixture</title>\n' > core/http/react-ui/dist/index.html
  ```

  This fixture is not a production UI build or UI verification.
- Use a llama.cpp gRPC backend built from this candidate's source and patches.
  Record the executable/source hashes with your test results.
- Provide an existing compatible decision GGUF, such as the pinned Julia-1
  gallery artifact. The test itself performs no downloads or dependency installs.

## Run against an owned server

Set `BACKEND` to the native `grpc-server` executable and
`LOCALAI_DECISIONS_TEST_MODEL` to the existing GGUF's absolute path. If necessary,
set `LD_LIBRARY_PATH` to the backend's dependencies. Choose an unused loopback
port; never point this test at a shared backend, since it loads weights.

```sh
export LOCALAI_DECISIONS_TEST_GRPC=127.0.0.1:50193
"$BACKEND" --addr="$LOCALAI_DECISIONS_TEST_GRPC" > native-server.log 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || :; wait "$pid" 2>/dev/null || :' EXIT INT TERM

go test -count=1 -v ./core/http/middleware \
  -ginkgo.focus='native decisions integration' -ginkgo.v
```

The backend loader performs health checks before loading. The caller owns server
cleanup; the spec does not shut down an externally configured process. Its
request context is bounded to two minutes. Without either environment variable,
the spec skips and ordinary unit test runs need no native backend.

Success prints `native contract:` (answer and token usage) and `native routing:`
(probabilities, both labels, and `combined-target`). Retain those outputs and
server logs with the executable and model hashes. Normal uncached package tests,
race checks, and lint remain separate gates; this smoke test does not replace
them.
