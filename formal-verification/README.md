# Formal verification

Formal designs expressed as FizzBee specs.
Most specs cover the realtime API state machines; `model_loader_shutdown.fizz`
covers the shared model-loader lifecycle that all modalities use. Realtime
background and rationale:
[../docs/design/realtime-state-machines.md](../docs/design/realtime-state-machines.md)
(Part 6).

The designs are **authoritative**: behaviour changes go through the spec first,
then the implementation is checked against them.

## What's here

| File | Role |
|------|------|
| `response_lifecycle.fizz` | **Authoritative** FizzBee model of machine M3 (response coordination). Model-checked + drives the Go MBT conformance harness. |
| `turn_lifecycle.fizz` | **Authoritative** FizzBee model of machine M2 (turn detection): the speechStarted / turn-open coupling. |
| `conn_lifecycle.fizz` | **Authoritative** FizzBee model of machine M1 (connection lifecycle): VAD toggle + once-only teardown. |
| `compaction.fizz` | **Authoritative** FizzBee model of machine M4 (conversation compaction): single-flight. |
| `tts_pipeline.fizz` | **Authoritative** FizzBee model of machine M5 (TTS pipeline): open->closing->closed, idempotent close. |
| `session_lifecycle.fizz` | **Composition** spec: the M1–M5 hierarchy — conn (M1) is the parent; when it is torn down, every child (vad/M2, resp/M3, compaction/M4) is terminal. Models the relationship the per-machine specs can't express. |
| `model_loader_shutdown.fizz` | **Authoritative** shared lifecycle design: bounded busy/Free waits, distributed force propagation/port reservation, and parallel in-flight accounting. Checked via `make test-model-lifecycle-conformance`. |
| `fizzbee.sha256` | Pinned checksum(s) of the FizzBee release the gate uses (created on first `install-fizzbee.sh` run). |

The implementations under test live in
[`core/http/endpoints/openai/respcoord`](../../../core/http/endpoints/openai/respcoord) (M3),
[`core/http/endpoints/openai/turncoord`](../../../core/http/endpoints/openai/turncoord) (M2),
[`core/http/endpoints/openai/conncoord`](../../../core/http/endpoints/openai/conncoord) (M1),
[`core/http/endpoints/openai/compactcoord`](../../../core/http/endpoints/openai/compactcoord) (M4),
and [`core/http/endpoints/openai/ttscoord`](../../../core/http/endpoints/openai/ttscoord) (M5).

## Running the realtime gate

```sh
make test-realtime-conformance
# or directly:
./scripts/realtime-conformance.sh
```

Two layers, **both required — the gate is fail-closed**:

1. **Go-native conformance** — the `respcoord` + `turncoord` + `conncoord` + `compactcoord` + `ttscoord` transition-table
   tests + Ginkgo/Gomega seeded property (random-walk) tests under `-race`
   (checks the implementation), plus the shared `coordinator` runtime they all
   build on. Also run as part of `make test` (they're ordinary Go packages with a
   Ginkgo suite each). The five machines reduce to their sealed State/Event/Effect
   types + a pure `Next`; the single-writer Coordinator/Sink plumbing lives once in
   `core/http/endpoints/openai/coordinator` (a generic `Coordinator[S,E,F]`).
2. **FizzBee model check** — model-checks the authoritative `.fizz` specs (checks
   the design). **A missing FizzBee is a hard failure, not a skip** — otherwise
   the design verification silently disappears whenever the tool is inconvenient,
   which is the whole thing we're trying to prevent.

FizzBee is pinned and checksum-verified (`fizzbee.sha256`), so "couldn't install"
is not a reason to skip — run `make install-fizzbee`. The **only** way to skip is
the explicit, loud `REALTIME_CONFORMANCE_SKIP_FIZZBEE=1` opt-out, intended for
local work on unrelated code. CI never sets it.

## Running the model-loader lifecycle gate

```sh
make test-model-lifecycle-conformance
# or directly:
./scripts/model-lifecycle-conformance.sh
```

This focused, fail-closed gate runs the loader, gRPC client, distributed
unloader, and worker lifecycle specs under the race detector and then checks
`model_loader_shutdown.fizz`. The model proves that local force and bounded
graceful busy waiting cannot wedge unrelated loads, distributed force skips
`Free()` and reserves the worker port until termination, and parallel request
tracking stays busy until the final request completes. Process termination is
an explicit fairness assumption; the concrete Go tests connect the abstract
model to the implementation paths.

## Installing FizzBee (pinned)

FizzBee is pre-1.0 and single-maintainer, so we pin a version + sha256 and use the
prebuilt release tarball (its primary build is Bazel — it is **not** go-gettable:
the `pkg/modelchecker` library imports the Bazel-internal `fizz/proto` with no
committed `.pb.go`, so a plain `go get` won't build it).

```sh
make install-fizzbee                  # = scripts/install-fizzbee.sh (default v0.5.2)
```

The four platform assets are pinned by sha256 in `fizzbee.sha256` (digests taken
from the GitHub release); the installer verifies before extracting. Heads-up: the
Linux bundles are large (~290–350 MB, because `parser_bin` embeds a full runtime),
macOS ~36 MB. CI caches `.tools/fizzbee` keyed on the pin so it downloads once.

This unpacks a **self-contained** directory under `.tools/fizzbee/` (gitignored):

```
.tools/fizzbee/
  fizz                              -> stable symlink the gate auto-detects
  fizzbee-v0.5.2-linux_x86/
    fizz            # CLI wrapper (entrypoint)
    parser/parser_bin # the .fizz frontend, BUNDLED (no system Python needed)
    fizzbee         # Go model-checker binary
    fizz.env        # resolves the above paths relative to `fizz`
    mbt_gen.zip     # MBT generator (this one DOES need system python)
```

Keep the directory intact — `fizz.env` resolves its siblings relative to the
`fizz` wrapper. The gate auto-detects `.tools/fizzbee/fizz`; override with
`FIZZBEE_BIN` only if you installed elsewhere (point it at the `fizz` wrapper,
not the raw `fizzbee` binary).

First `install-fizzbee.sh` run prints the computed sha256; record it in
`fizzbee.sha256` as `<sha256>  <asset>` and commit so later runs verify the pin.

> CLI facts (validate against the pinned version — FizzBee is pre-1.0): the CLI
> is `fizz [flags] <spec.fizz>` (default = exhaustive BFS); there is **no `run`
> subcommand**. The checker can print `FAILED`/`DEADLOCK` while still exiting 0,
> so the gate scans output for those markers in addition to the exit code.
> Model-checking needs only the bundled `parser_bin` (no Python); only
> `mbt-scaffold` shells out to system `python`.

## Reproducing the bug the spec catches

Each spec models the **correct** design, so it passes; each documents how to
reproduce the legacy bug it guards against:

- `response_lifecycle.fizz` (M3): change `atomic func start()` to
  `serial func start()` — the checker reports `AtMostOneLive` violated (the
  dual-writer race). Pinned deterministically in Go by the respcoord
  "legacy dual-writer characterization" spec.
- `turn_lifecycle.fizz` (M2): in `Abort`, delete `self.speech = 0` (clear only
  the turn, as the legacy `discardTurn` did) — the checker reports `Coupled`
  violated (the speechStarted/turn-open desync that suppressed the next onset).
- `conn_lifecycle.fizz` (M1): in `Close`, delete `self.torn = 1` — the checker
  reports `TeardownOnce` violated (the legacy double-teardown / double-close
  hazard when a session reaches teardown from more than one exit path).
- `compaction.fizz` (M4): in `Trigger`, delete the `if self.active == 0:` guard —
  the checker reports `SingleFlight` violated (two goroutines compacting the same
  overflow concurrently, the race the `compacting` CAS prevents).
- `tts_pipeline.fizz` (M5): in `Close`, delete the `if self.phase == 0` guard —
  the checker reports `WakeOnce` violated (a non-idempotent wait() that wakes /
  joins the worker more than once).
- `session_lifecycle.fizz` (hierarchy): in `Teardown`, delete `self.compaction = 2`
  — the checker reports `ChildrenDieWithParent` violated. This is the real M4 gap:
  a fire-and-forget compaction outliving the torn session. The fix is `conncoord`'s
  teardown cancelling + joining each conversation's compaction (and respcoord/
  compactcoord gained an absorbing `Terminated` state so no child can start after
  teardown).
- `model_loader_shutdown.fizz`: in `ForceShutdown`, delete `self.backend = 2` to
  violate local progress; move `self.port_recycled = 1` from `ProcessStops` to
  `WorkerReceivesStop` to violate distributed port safety; or set `self.busy = 0`
  in `FinishOne` to violate parallel busy accounting. The checker rejects each
  mutation.

## Adding another machine

All five mapped machines (M1–M5) have landed. To add a new sealed-state machine:

1. Add `<machine>.fizz` here (with an `always assertion`; verify non-vacuity by
   breaking one guard and confirming the checker fails).
2. Implement it as a sealed-state package under `core/http/endpoints/openai/`.
3. Add transition-table + Ginkgo/Gomega seeded property conformance tests
   (one `*_suite_test.go` bootstrap per package; LocalAI mandates Ginkgo/Gomega).
4. The gate picks up new `*.fizz` specs automatically; add the new Go package to
   the `-race` test list in `scripts/realtime-conformance.sh` and the path
   filters in `.github/workflows/realtime-conformance.yml`.
