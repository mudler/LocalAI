# Execution status model and trace viewer

This pure model describes one enclosing request's status observation, not
execution authority. Bindings include actor, credential, conversation, provider, generation,
incarnation and tool names. `Reduce` returns a state, ordered
effects, rule and guard. It does not perform I/O or read a clock.

`Traces()` supplies independent expected rules, states, ordered effects and final
projections for all eleven normative trace families. `RunTrace` runs the actual
reducer. `Explore(6)` checks bounded histories, safety, liveness and all 49
state/event cells. R8 is retired; ignored transitions use I. A settled failure
cannot retry without ending the binding lifetime through authoritative admission.

## Generate and inspect

From the repository root:

```sh
go test -count=1 ./pkg/addons/model/executionstatus/...
go run ./pkg/addons/model/executionstatus/cmd/viewer -check
go run ./pkg/addons/model/executionstatus/cmd/viewer -o /path/to/executionstatus-viewer.html
```

Open the resulting HTML directly in a browser. Select a trace and use Previous
and Next to inspect each event, before/after state, ordered effects, rule and
guard. The page is self-contained: no server, network, JavaScript reducer or
frontend build is needed. JavaScript only presents transitions computed by Go.
Reply descriptions retain the model payload's validation details; they are not
wire-format payloads. Fixtures contain synthetic opaque identities, not secrets.

Without `-o`, generation writes `executionstatus-viewer.html` under
`SDD_LOCAL_ROOT` when that environment variable is present. Otherwise it writes
portable `viewer.html` in the current directory.
`go generate ./pkg/addons/model/executionstatus` uses the same default: the
configured artifact root, or the model directory when the variable is absent.
An explicit `-o` always overrides this default. Generated HTML is not committed.

## Check contract

`-check` writes nothing. It recomputes real traces, compares each step with the
independent expected rule/state/ordered effects, checks final projections and
nonempty guards, and requires all families, rules, states and effect kinds.
It runs shared bounded invariant exploration, including complete matrix coverage.
It then generates twice in memory, compares bytes for determinism and checks the
embedded JSON against freshly computed data. Map keys are sorted by Go's JSON
encoder; HTML-sensitive payload text is escaped.

To verify an existing generated artifact, supply both flags:

```sh
go run ./pkg/addons/model/executionstatus/cmd/viewer -check -o /path/to/executionstatus-viewer.html
```

This additionally checks embedded data and exact generated HTML bytes, detecting
stale data or presentation. A missing file, stale file, invariant counterexample,
trace mismatch, incomplete coverage, nondeterminism or malformed template returns
nonzero. Bare `-check` deliberately needs no existing artifact; it verifies the
current model and template in memory. Regenerate and check after changing the
reducer, fixtures, exploration domain or viewer template.

The viewer's Ginkgo/Gomega suite tests data correspondence and error paths,
including changed expected rules/states/effects, missing families, invariant
failure, incomplete exploration, deterministic HTML, stale files and CLI errors.
The model suite remains the independent source of reducer and constructor checks.
These bounded checks do not certify runtime security or wire contracts.
