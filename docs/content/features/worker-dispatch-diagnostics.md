+++
title = "Worker dispatch diagnostics"
weight = 40
+++

The llama.cpp gRPC adapter can record request-correlated dispatch milestones for
`PredictStream`. Diagnostics are disabled by default.

## Enable diagnostics

Set `LOCALAI_WORKER_DIAGNOSTICS=1` in the worker process environment before startup.
The service reads this setting once during construction. Other values do not enable
collection. Restart the worker without this setting to disable collection.

Each request also needs exactly one `localai-diagnostic-id` gRPC metadata value.
The value must be a canonical, lowercase UUIDv4 with the RFC UUID variant.
The adapter rejects duplicate, malformed, oversized, uppercase, and NUL-containing
values for diagnostic collection. This does not reject the inference request.
Metadata cannot enable diagnostics without the worker setting.

The identifier provides request correlation only. It does not authenticate callers
or identify connections, attempts, users, or models. The worker records every
request that satisfies both conditions; it does not add independent sampling.

## Recorded boundaries

| Phase | Boundary |
|---|---|
| `worker_receive` | Handler entry, after metadata validation |
| `worker_prepare_start` | Before option parsing, after auth and model checks |
| `worker_prepare_end` | Tasks are ready for submission |
| `worker_post_start` | Immediately before `post_tasks` |
| `worker_post_end` | Immediately after `post_tasks` returns |
| `worker_first_write_attempt` | Immediately before the first actual gRPC write |
| `worker_first_write_complete` | Immediately after that write returns |

The first write can contain only a role announcement. Null begin markers do not
count as writes. A failed first write records `failed`; a successful write records
`ok`. Other phases record `none`. Early returns leave later milestones absent.
A missing completion can indicate an operation that did not return normally.

Records contain only `diagnostic_id`, `phase`, `offset_ns`, `outcome`, and
`dropped_events`. They contain no prompts, token text, metadata dumps, or errors.
The collector stores at most seven events per request in fixed storage.
Each phase occurs at most once. Duplicate milestones are suppressed, not dropped;
the closed schema fits the entire buffer, so `dropped_events` is zero.

Offsets use the process monotonic clock relative to this request's collection
start. Do not subtract these offsets from another process's clock.
Write completion does not mean client delivery. Handler entry does not mean socket
arrival. Preparation and submission durations exclude result waiting.

The adapter emits JSON lines to standard error during request teardown, after
response-reader cleanup. No diagnostic log writes occur inside measured operations.
Emission remains synchronous: a slow log destination can delay handler return.
Clock reads and collection also add overhead. These records do not quantify that
overhead; measure it separately before attributing small latency differences.

## Verification

Run the standalone helper gate without downloading dependencies:

```sh
NLOHMANN_INCLUDE=/usr/include CXX=g++ make test-backend-cpp
```

The standalone runner discovers `dispatch_diagnostics_test.cpp`.
It intentionally does not discover `dispatch_correlation_fixture.cpp`.
The helper captures actual standard-error emission, checks the closed record schema,
and checks distinct UUIDs from concurrent collectors. It checks sentinel exclusion
and exception propagation with no write-completion event after a throwing writer.
These checks exercise the collector, not the full gRPC handler.

The helper prints overhead distributions for disabled collection, enabled collection,
and enabled collection with emission to a local file. Each distribution contains
101 batch means, with 100 requests per batch. Each request includes construction,
seven milestones, and destruction. Results are informational, with no timing threshold.
They exclude metadata validation, gRPC, engine work, and production log transport.
Compiler optimization and the log destination affect these measurements.

The linked fixture includes the actual adapter translation unit, excluding only
its `main`. It calls the same array/object/null streaming seam as production.
It covers role ordering, subsequent object and array writes, first-write failure,
and null begin markers. It also calls the actual handler for auth and no-model errors.
Its peer-state assertions are not proof of handler cancellation or slot cleanup.
Those require an engine-backed cancellation test with the matched dependencies.

For an already prepared, version-matched engine tree, add
`-DLLAMA_GRPC_BUILD_TESTS=ON` to its existing configure command.
Build `dispatch_diagnostics_test` and `dispatch_correlation_fixture`, then run:

```sh
ctest --test-dir <configured-build-directory> \
  --no-tests=error --output-on-failure -R '^dispatch_(diagnostics_test|correlation_fixture)$'
```

Preserve the engine revision and apply the existing patches once per clean engine
checkout. `prepare.sh` stages the fixture but does not fetch its dependencies.
A helper-only pass does not verify adapter compilation, gRPC cancellation, engine
cleanup, or runtime compatibility. A host CPU build is not equivalent to an ARM64
CUDA build. The linked fixture and runtime checks remain mandatory gates.
