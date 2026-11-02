# Agent memory: bounded trigger-to-Slack acceptance

`core/application/studio_memory_transport_test.go` runs the same two table
cases against actual Chromem and PostgreSQL collection engines. It follows:

1. `studio.NewTriggers`, `CreateTrigger` and a due `Pass` through the runner's
   actual start path into a saved curator agent's trigger-origin chat.
2. The accepted trigger prompt through the durable capture store and
   `ProcessMemoryCaptures`, then the application `studioCollections` adapter
   into the chosen engine. The curator uses `incoming_only` automatic capture
   without enabling `add_memory`.
3. Slack Socket Mode events from `slacktest.Server`, through the real Slack
   add-on runtime, `studiobot` transport, connector manager and a saved binding
   into a separate search-only agent's chat owned by the same principal.
4. `studiotest.ToolHarness` invokes the actual `search_collection` host tool
   (`SearchMemoryTool`), asserting both the captured text and exact backend
   entry ID. Add/delete tool invocations fail because those tools are absent.

The cases also replay the same trigger due slot and the same Slack message
identity (the latter through the public `Connectors.Ingest` ingress used by
the poll loop), verifying one curator capture and one backend entry, with no
second Slack turn. Revoking the peer's search grant denies the next Socket
Mode message on the already-bound chat. A second principal's separate Slack
binding cannot use an agent association to read the first principal's entry.

The embedding HTTP response and model script are deterministic fixtures. The
remote Slack platform is simulated, but its runtime and core connector path
are real. Chat, agent, trigger and binding stores are JSON. These cases do not
claim external Slack connectivity, HTTP webhook authentication, SQL chat-store
coverage, multi-frontend coordination or the full crash/restart matrix. No
production test-only API or fabricated capture queue records are used.

## Run

Set `MEMORY_DATABASE_URL` to a live PostgreSQL instance with the extensions
required by the collection engine. Set `LOCALAI_TEST_POSTGRES_URL` to that same
instance when running adjacent PostgreSQL suites. The local acceptance fixture
used PostgreSQL 17 on port 5433 in `localrecall-memory-task3`; the container's
default PostgreSQL 16 listener is not that fixture. Resolve its current IP
rather than assuming a fixed bridge address.

```sh
export GOMAXPROCS=2 GOFLAGS=-p=2
# Set TMPDIR and GOTMPDIR to the work area's existing tmp directory if needed.
for run in 1 2 3; do
  go test -race -tags auth ./core/application -run TestApplication \
    -ginkgo.focus='trigger-to-Slack transport acceptance' -ginkgo.v -count=1 \
    || exit "$?"
done
```

Without `MEMORY_DATABASE_URL` the PostgreSQL case skips: that is not evidence
of real PostgreSQL acceptance. Repeat using separate `-count=1` invocations;
Ginkgo rejects `go test -count=3`, and the test binary does not expose the
Ginkgo CLI's `-repeat` option as `-ginkgo.repeat`.
