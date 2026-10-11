+++
disableToc = false
title = "Profiling and request diagnostics"
weight = 30
+++

LocalAI provides two independently opt-in diagnostics: a private Go profiling listener and structured phase timing events.
Both default to disabled. Debug logging alone enables neither feature.

## Scope and configuration

These options apply to `local-ai run`, including a distributed frontend.
They do not enable diagnostics in separately launched workers or provide backend-native llama.cpp profiling.
Diagnostics has no MCP, admin, or public API surface and no UI controls.
State is per-instance, not shared between replicas. Profiles describe the frontend Go process, not the whole cluster.

| Parameter | Default | Description | Environment Variable |
|-----------|---------|-------------|----------------------|
| `--pprof` | `false` | Enable the private loopback profiling listener | `$LOCALAI_PPROF` |
| `--pprof-address` | `127.0.0.1:6060` | Numeric loopback IP with an explicit port | `$LOCALAI_PPROF_ADDRESS` |
| `--pprof-mutex-profile-fraction` | `0` | Nonnegative mutex sampling fraction; zero disables sampling | `$LOCALAI_PPROF_MUTEX_PROFILE_FRACTION` |
| `--pprof-block-profile-rate` | `0` | Nonnegative block sampling rate in nanoseconds; zero disables sampling | `$LOCALAI_PPROF_BLOCK_PROFILE_RATE` |
| `--request-phase-timing` | `false` | Emit sanitized phase events at Info, independently of profiling | `$LOCALAI_REQUEST_PHASE_TIMING` |

The address accepts numeric IPv4 loopback or bracketed IPv6 loopback, such as `127.0.0.1:6060` or `[::1]:6060`.
Ports must be decimal values from 1–65535.
Validation rejects hostnames, including `localhost`, wildcard addresses, empty hosts, LAN addresses, public addresses, and Unix sockets.
Validation applies even when profiling is disabled. Negative sampling settings fail validation; nonzero sampling settings require `--pprof`.
`--preload-backend-only` validates the options but does not start profiling or sampling.

For mutex sampling, a value of N records approximately one in N contention events; 1 records every event.
Block sampling targets one blocking event per configured number of nanoseconds spent blocked; 1 records every blocking event.
These settings are not percentages or capture durations. Sampling adds process-wide overhead, especially at 1.

One diagnostics server owns process-global runtime sampling in each run process.
It applies both configured rates once after binding successfully and resets both rates to zero on shutdown or fatal serving failure.
It does not restore previous rates; Go provides no getter for the previous block rate.
Do not combine competing sampling owners in the same process.
There is no dynamic toggle, continuous capture, or automatic profile storage. Configuration changes require a separately approved restart or rollout.

The listener binds before normal request serving. Validation or bind errors fail startup; subsequent fatal serving errors propagate through the run lifecycle.
The private listener gets five seconds to drain before profiling connections are force-closed, which can interrupt an active capture.
Its cleanup runs on startup failure, normal return, and through the existing signal handler (which exits without running deferred cleanup).
A fatal profiler serving error cancels the run context and is returned after application cleanup.
Application initialization or shutdown work that ignores cancellation can delay that return; the private-listener budget is not a deadline for the entire application.
With profiling disabled, diagnostics opens no socket, starts no monitoring goroutine, and changes no runtime sampling settings.
Disabled phase timing allocates no diagnostic identities or events and adds no diagnostic filesystem operations.

## Access and data sensitivity

{{% notice warning %}}
The profiling listener is unauthenticated. Loopback reachability is its authorization boundary, not the public API's authentication policy.
Other processes in the same network namespace can access it, including containers sharing a pod's namespace.
Profiles can contain sensitive application data. Heap data and goroutine output can expose secrets.
Protect captures, review them before sharing, and delete them when the investigation ends.
{{% /notice %}}

The separate listener uses a private mux, not the public API router or the default Go mux.
It provides the profiling index, CPU, trace, symbol, and named runtime profiles, including heap, goroutine, mutex, and block.
`/debug/pprof/cmdline` is unavailable, including query and subtree variants, because command-line arguments can contain credentials.
The standard index can still mention that endpoint.

Timing events omit credentials, bearer candidates, model names, prompts, bodies, raw headers, filenames, paths, and raw errors.
This sanitation applies only to diagnostic timing payloads. It does not redact unrelated logs or profiles.
Events use the constant message `diagnostic_phase` at Info.
Operator-selected warn/error filtering suppresses these events, even when phase timing is enabled.

## Bounded Kubernetes investigation

A rollout or build configuration change is a separate, explicit, owner-approved action.
Select one frontend and a defined observation window. Approve profiling, timing, and any nonzero sampling settings before the window.
Do not change replicas, clocks, scheduling, or storage mounts to force a result.
The examples below capture data only; they do not mutate a live deployment.

Your cluster runtime must support port-forwarding to the pod's loopback listener.
Do not add a Service, ingress, or NodePort for profiling.
Forward directly to the selected frontend pod, not a load-balanced Service.
The local port must be free. Keep kubectl's default local-only binding; do not add a wildcard `--address`.

1. Start the direct pod port-forward in a separate terminal, replacing both placeholders:

   ```sh
   kubectl -n <namespace> port-forward pod/<selected-frontend-pod> 6060:6060
   ```

2. Protect newly created files in your capture terminal:

   ```sh
   umask 077
   ```

3. Capture ten seconds of CPU activity during the approved window:

   ```sh
   curl --fail --output cpu.pprof 'http://127.0.0.1:6060/debug/pprof/profile?seconds=10'
   go tool pprof cpu.pprof
   ```

CPU profiles show execution, not time blocked on I/O or locks.
Capture naturally slow requests and matched control requests against the same frontend.
Record the observation window and generated diagnostic IDs alongside protected captures.
Profiles are process-wide; they do not automatically attribute samples to a request ID.

Mutex and block profiles require their respective nonzero sampling settings before capture.
The following ten-second delta captures bound collection time, not the duration of process-global sampling.
Sequential commands cover different windows; compare them accordingly.

```sh
curl --fail --max-time 20 --output mutex.pprof 'http://127.0.0.1:6060/debug/pprof/mutex?seconds=10'
curl --fail --max-time 20 --output block.pprof 'http://127.0.0.1:6060/debug/pprof/block?seconds=10'
go tool pprof mutex.pprof
go tool pprof block.pprof
```

Mutex profiles locate contention attributed to lock holders; block profiles locate sampled blocking operations.
Neither substitutes for the request-owned lock and filesystem timings below.
A goroutine snapshot can show I/O or lock waits that CPU profiles miss.
It describes one instant, not a measured duration. Its text can be large and sensitive; the client timeout bounds waiting, not file size.

```sh
curl --fail --max-time 10 --output goroutines.txt 'http://127.0.0.1:6060/debug/pprof/goroutine?debug=2'
```

Stop the port-forward after capture. Disable diagnostics and sampling afterward through the separately approved rollout process.
Protect, review, and delete the capture files under your data-handling policy.
If latency does not reproduce, report non-reproduction. Do not infer a predetermined filesystem, lock, or routing root cause.

## Event identity and phase boundaries

Each event contains `id`, `kind`, `phase`, `state`, `outcome`, `elapsed`, and `count`.
The server generates diagnostic UUIDs; it does not trust or reuse client `X-Correlation-ID` headers as diagnostic identities.
Requests use kind `request`; config reload operations use kind `config_reload` with fresh operation IDs.
Request identity survives the instrumented internal context reconstructions. Background callers remain unobserved unless explicitly enabled.

States are `start`, `end`, and `mark`; outcomes are `ok`, `error`, `canceled`, and `skipped`.
Durations use monotonic elapsed time, not cross-host clock subtraction.
Counts describe aggregate work, not filenames or model labels.
Lock-protected helpers emit buffered aggregates after unlocking, so log order alone does not establish execution order.

Nested timings overlap and are not additive. Repeated extraction segments share one request ID; they are not duplicate whole-request measurements.
Extraction ends before downstream inference. It is not socket arrival or full request latency.
The existing trace start follows global authentication and body reading; it is not socket arrival either.

| Phase | Measured boundary |
|-------|-------------------|
| `extraction` | Each extractor-owned entry/exit segment, including repeated extraction or retries, before downstream inference. |
| `bearer_lookup` | Existing bearer-derived model existence lookup, without recording the candidate. |
| `default_listing` | Existing default-model candidate listing within extraction. |
| `body_lookup` | Existing body-derived model existence lookup, without recording body data. |
| `config_load_defaults` | Existing config loading and default application within extraction. |
| `alias_resolution` | Existing model alias resolution within extraction. |
| `config_lock_wait` | Time waiting to acquire the config loader mutex for filtering. |
| `config_lock_hold` | Time holding that mutex for the config listing/filter operation. Includes nested filter work. |
| `config_filter` | The in-memory config filter pass under that lock, not filesystem latency. |
| `fs_enumeration` | The existing `os.ReadDir` call for loose model discovery only. |
| `loose_filter` | Existing filename and in-memory loose-file filtering passes, not directory enumeration. |
| `existence_fallback` | The existing `ExistsInModelPath` fallback when discovery requires it. No extra diagnostic filesystem calls. |
| `model_init` | Entry mark immediately after `ModelLoader.Load` constructs options, before admission, cache, or load logic. Not backend-native execution. |
| `model_router_callback` | Only the existing `ModelRouter` callback invocation. Not transport publish or worker receive latency. |
| `reload_lock_wait` | Time waiting for the mutex of the loader performing the reload. |
| `reload_lock_hold` | Time holding that loader's mutex during reload, including nested enumeration, metadata, and later parsing work. |
| `reload_enumeration` | The reload's existing `os.ReadDir` call. |
| `reload_metadata` | Aggregate existing `DirEntry.Info` calls, separate from enumeration. |
| `reload_yaml_read` | Aggregate YAML file reads, including configured-gallery YAML inspection where applicable. |
| `reload_parse_defaults` | Aggregate YAML parsing/default application, including configured-gallery YAML classification where applicable. Excludes the measured file read. |

### Reload observer boundaries

Reload timing measures `ModelConfigLoader.loadModelConfigsFromPath`, not the unrelated dynamic JSON watcher.
Distributed resync constructs a temporary authoritative loader with its own mutex.
That loader's reload events do not prove contention on the shared loader lock or ownership of another request's wait.
No event establishes cross-host timing or worker execution.

The immutable constructor option `WithReloadDiagnostics` controls early lock, enumeration, and metadata timing.
Ordinary `LoadOptionDiagnostics` callbacks run at their original position after enumeration and metadata, under the loader lock.
Their resolved recorder controls later YAML-read and parse/default timing.
An option-only caller cannot observe earlier phases. A late nil recorder disables subsequent instrumented work, not constructor-enabled early measurements.

Unchanged recorder identity preserves the reload operation ID across early and later phases.
A different non-nil late recorder receives a separate reload operation ID; these are separate observations, not one correlated span.
Callbacks retain ordering, composition, and last-write semantics.
They retain every preexisting invocation, including each parsed model's `ModelConfig.SetDefaults`; diagnostics does not add a replay or snapshot functional options.

See the [CLI reference]({{% relref "reference/cli-reference" %}}) for other server options.
The [troubleshooting guide]({{% relref "getting-started/troubleshooting" %}}) covers broader operational checks.

### Response correlation

With `--request-phase-timing` (`LOCALAI_REQUEST_PHASE_TIMING=true`), HTTP requests receive `X-LocalAI-Diagnostic-ID`.
The header contains the same server-generated UUID v4 as that request's phase events.
LocalAI sets it at the first application middleware, before authentication, path rewriting, extraction errors, or streaming output can write the response.
Repeated extraction and failover attempts retain one ID for the logical request.

Client-supplied diagnostic or correlation headers do not select this ID.
The ID contains no model name, authentication token, or request content.
It is a correlation value, not an authorization credential or a distributed trace ID.
Requests rejected before extraction, including global authentication failures, retain this header.
Disabling request-phase timing emits no diagnostic header and creates no diagnostic identity.
Enabling pprof or debug logging alone does not enable the header.

Go callers can use `diagnostics.RequestID(ctx)` to read the existing request ID without creating one.
The accessor returns an empty string for nil contexts, unobserved contexts, and background reload observations.

### Chat model fallback

OpenAI chat completions bind the request before considering legacy model discovery.
An explicit bound model skips Bearer-as-model lookup and default candidate listing; model existence validation still runs.
If the bound model is empty, extraction retains context, path, query, form, Bearer, and first-available fallback precedence.
The existing candidate filter and ordering still select the default.
Malformed JSON returns a parsing error without fallback discovery.
Each failover attempt binds fresh input from the replayed body.
Other API routes retain their existing extraction order.

### Passive gRPC dispatch observations

Request timing observes the existing `ModelMetadata`, `Health`, `Predict`, and
`PredictStream` calls. Each call still owns a lazy `grpc.NewClient` and closes it
at its original boundary. Diagnostics does not connect early, wait for readiness,
force headers, replace the dialer, pool connections, or send additional RPCs.
Resolver, proxy, credentials, and retry configuration remain unchanged.

The outgoing wire format is one lowercase metadata key, `localai-diagnostic-id`,
with one canonical lowercase RFC4122 UUID v4 value (36 ASCII characters, hyphens
at positions 9, 14, 19, and 24). It is the server-generated request identity, not
an incoming HTTP header. Outgoing metadata is copied and only this key replaced;
authentication metadata and per-RPC credentials remain intact. Disabled or
unobserved requests leave metadata unchanged. No attempt key is transmitted in
this version: worker correlation is request-level; frontend numeric attempts
remain local. Workers must independently opt in and validate the UUID before
recording it; this metadata is not authentication.

`client_lock_wait` measures only acquisition of the existing non-parallel client
lock. `rpc` and its allowlisted method child (`rpc_metadata`, `rpc_health`,
`rpc_predict`, `rpc_predict_stream`) start at gRPC TagRPC and end at stats End,
with count one per observed transport RPC attempt. Transparent retries may create
multiple observations; these are not extra diagnostic RPCs. Durations include
transport waiting and overlap; do not add parent and child durations.

`rpc_sent` is the first outgoing payload stats observation, not proof of worker
receipt. `rpc_headers` and `rpc_first_message` observe the first incoming headers
and decoded payload without forcing reads. `transport_ready` is the first actual
ConnBegin observation for that ClientConn, not NewClient construction or a TCP
connection attempt. A request-local opaque numeric connection identity is captured
by the handler because connection contexts do not carry RPC contexts. Reconnects
share the ClientConn identity; readiness is a first milestone, not a transport
inventory. `dial_attempt` with outcome `skipped` explicitly means TCP dial-attempt
detail is unavailable. TCP, proxy, TLS and HTTP/2 handshake substeps are not
separately measurable with these passive hooks. Despite the legacy phase name,
ConnBegin does not guarantee peer HTTP/2 handshake completion or RPC readiness.

| Phase | Measured boundary |
|-------|-------------------|
| `rpc_sent` | First outgoing payload stats observation. |
| `rpc_health` | Health transport RPC attempt through stats End. |
| `rpc_metadata` | ModelMetadata transport RPC attempt through stats End. |
| `rpc_predict` | Predict transport RPC attempt through stats End. |
| `rpc_predict_stream` | PredictStream transport RPC attempt through stats End. |

Actual canceled and deadline-exceeded gRPC status errors use outcome `canceled`;
other errors use `error`, with no error text recorded. EOF is successful stream
completion. These transport events are not frontend callback or SSE delivery
measurements. No addresses, headers, payloads or credentials enter events.

Collection is bounded by the dispatch lifetime budget. After the wrapper releases
its locks and closes its connection, an asynchronous drain attempts nonblocking
admission to a **process-wide limit of 16 owners, with no waiting queue**.
Admission happens before goroutine creation. Late stats callbacks coalesce with
an active owner or attempt fresh admission after that owner exits. Saturation
permanently abandons that handler's asynchronous flushing; callbacks do not retry
admission for an abandoned handler. No goroutine is created for rejected work.
Each owner performs at most 257 drain passes before abandoning repeated pending
work. This is per-instance diagnostic state, not shared across frontend replicas.

`grpc.DiagnosticFlushDrops()` reads an atomic process-local cumulative count of
abandoned handlers (saturation or exhausted drain passes), without calling the
sink. It remains accessible when the sink stalls, resets at process restart, and
counts neither events nor unique requests (one request can use multiple handlers).
It is separate from per-request `dispatch_summary` collection-budget drops; no
new log or HTTP endpoint is introduced. Abandonment does not discard the shared
request collection: another explicit synchronous flush may still emit it.

The synchronous sink cannot be canceled: up to 16 owners and their retained
request contexts/collections can remain alive indefinitely if it never returns.
There are zero queued request contexts; active RPCs retain their own state as
usual. This is a bound on asynchronous ownership, not a wall-clock lifetime
promise or a bound on all active requests. Capacity recovers when sinks return;
there is no timeout goroutine, background retry or shutdown drain guarantee.
Sinks never execute on transport callbacks and no gRPC inference work waits for
this flushing. The existing synchronous diagnostics APIs are unchanged and can
still block their callers. Events may be logged after the HTTP summary or caller
return, and process termination can lose pending events. Use offsets and
identities rather than log order, and account for sink time and both drop counts.

### Dispatch contract for Go integrations

HTTP admission records `http_entry` at the first application middleware and an
`auth` span ending immediately before the authenticated, exempt, or auth-disabled
handoff (or at rejection). Downstream inference time and errors are not auth work.
When existing API tracing reads a JSON body, `body_read` marks that read's
completion, including errors; `trace_start` marks the existing trace start after
a successful read. Disabled tracing and excluded bodies add no reads or marks.
These are application boundaries, not socket arrival or universal body ingestion.

The outer HTTP middleware drains the bounded dispatch collection after handler
and middleware unwinding, outside application locks. This is final for HTTP
admission producers; it is not a guarantee that detached inference callbacks have
finished. Joined streaming work is included. Future asynchronous observation
sites must arrange their own completion flush after their producers finish,
including early stream write failures; do not flush from cancellation alone.
Tracing's asynchronous persistence consumer produces no dispatch events.
No routing, gRPC, worker, or SSE observation sites are added here.
There is no request sampling control: all requests with an enabled recorder are
eligible. The mutex and block sampling settings above apply only to pprof.
Existing `Begin`, `Mark`, `Record`, and `Sink` calls remain synchronous, and legacy
log records keep their existing fields.

Dispatch state is per-request and per-instance. It is not replicated between
frontends. `Inherit` shares that request's identity, origin, and collection while
preserving destination cancellation and values. Cancellation never suppresses
collection; callers supply the categorical outcome explicitly.

```go
func (r *Recorder) AttachRequest(ctx context.Context, id string) (context.Context, bool)
func Dispatch(ctx context.Context) DispatchScope
func (s DispatchScope) NewAttempt() DispatchScope
func (s DispatchScope) NewConnection() DispatchScope
func (s DispatchScope) Start(phase Phase) (DispatchScope, func(Outcome, int))
func (s DispatchScope) MarkOnce(phase Phase, outcome Outcome)
func (s DispatchScope) Record(phase Phase, elapsed time.Duration, outcome Outcome, count int)
func FlushDispatch(ctx context.Context) DispatchStats
```

`AttachRequest` accepts only canonical lowercase RFC4122 UUID v4 values. It does
not replace an existing request with a conflicting identity. Invalid values,
nil contexts, and nil recorders return the unchanged context and false. Use it
only at an independently enabled worker boundary, never to accept an HTTP
client's choice of diagnostic identity. Validation is not authentication.
`RequestID` reads the attached identity without allocating a new one.

`Dispatch` returns an immutable scope; the zero value and disabled or reload
contexts are inert. Retain returned scopes explicitly when crossing helpers.
`NewAttempt` and `NewConnection` allocate numeric request-local identities;
reuse the connection scope for the same actual connection. Neither creates or
changes a transport. Zero means unspecified. Identities are not comparable
between requests or processes.

`Start` returns a child span and an exactly-once end closure. Its first end call
wins, including an invalid call (which emits nothing). `MarkOnce` accepts the
first valid milestone per phase/span/attempt/connection. `Record` buffers an
aggregate with the actual operation count. Invalid enums, negative durations,
and negative counts are rejected, not logged as raw errors.

Dispatch events add `offset`, `span`, `parent`, `attempt`, `connection`, `dropped`,
`emitted`, and `sink_elapsed` through the optional `Event.Dispatch` fields.
Offsets use the local monotonic request origin captured at attachment; span
elapsed times overlap and must not be summed as disjoint work. External UUID
attachment starts a new local clock origin. Never subtract these offsets across
hosts. Boundaries are captured before buffering or sink calls; log order is not
execution order under concurrency.

`MaxDispatchEvents` is 256 for the lifetime of a request, including start, end,
and milestone records. Span, attempt, and connection identities each have the
same limit. Exhausted identity allocation returns an inert scope. Overflow
increments a saturating drop counter; flushing does not restore capacity.
Milestone keys are bounded by accepted events. Collection starts no goroutines.

Call `FlushDispatch` outside application locks, after all producers finish for a
complete report. It synchronously drains buffered events to the existing sink
and emits `dispatch_summary` outside the event budget. Repeat flushes can report
later drops or drain late producers. Concurrent or reentrant flushes return the
current statistics and ask the active owner to drain again before relinquishing
ownership. This also supports sink reentry without deadlock. Producers must
still request a flush on completion; cancellation is not completion.

Routing emits nested revision, reservation, health-cache/probe, and last-used
boundaries. `health_cache` reports `skipped` when this caller used a cached or
coalesced result (these cannot be distinguished without modifying the cache),
`ok` for its successful probe, and `error` for an unhealthy result. Only the
caller actually probing emits `health_probe`. Last-used `ok` means the void
registry call returned; the interface does not expose database errors.

Metadata decision spans count calls to the backend interface, not actual wire
RPCs. Empty means a nil or zero-size protobuf response; errors have no empty or
nonempty marker. The concrete gRPC client owns actual RPC observations.
No metadata result is cached by diagnostics.

Tracking increments and completion decrements are separate spans. Reservation
release is recorded once inside the existing release guard. Tracking completion
and explicit route teardown flush after registry and release locks are released,
including when HTTP has already returned. They do not wait for cancellation or
change inference synchronization. Later gRPC instrumentation owns flushing any
producer that outlives the backend method; frontend/SSE instrumentation owns its
own completion flush after its final callback or write, including early failure.
These flushes share a bounded per-request collection and may emit cumulative
summaries before other producers have finished.

`DispatchScope.Context(ctx)` retains span and attempt parents only when the
scope and destination have the identical request observation. It preserves
context lifetime and values; disabled and cross-request attachment are no-ops.

`DispatchStats` contains cumulative `Emitted`, `Dropped`, and `SinkElapsed`.
Sink time measures synchronous dispatch event writes only, excluding summary
writes, collection overhead, and legacy event writes. It is a partial overhead
measurement, not a correction to subtract from nested timings. Slow sinks still
block the flushing caller. Measure remaining instrumentation uncertainty
separately. Missing boundaries after overflow do not establish absent work.

The closed dispatch vocabulary reserves the following boundaries. Availability
requires an integration at the actual operation; a phase name does not prove
that a transport hook can observe it.

| Phase | Intended boundary |
|-------|-------------------|
| `http_entry` | Application entry, not socket arrival. |
| `auth` | Authentication only, ending before downstream work. |
| `body_read` | Body-read completion. |
| `trace_start` | Existing trace entry. |
| `route` | Route entry and exit. |
| `route_revision` | Revision check. |
| `reservation` | Reservation operation. |
| `health_probe` | Actual health probe. |
| `health_cache` | Health-cache decision. |
| `last_used` | Last-used update. |
| `metadata_decision` | Decision to probe metadata. |
| `metadata_rpc` | Actual metadata RPC and call count. |
| `metadata_empty` | Nil or zero-size protobuf metadata response. |
| `metadata_nonempty` | Metadata response with any populated protobuf field. |
| `metadata_marker_absent` | Successful metadata result with an empty media marker (including nil responses). |
| `metadata_marker_present` | Successful metadata result with a nonempty media marker; never includes its content. |
| `inflight_increment` | Synchronous tracking increment. |
| `inflight_decrement` | Completion tracking decrement. |
| `reservation_release` | Reservation release, separate from decrement. |
| `client_lock_wait` | Client lock acquisition wait. |
| `dial_attempt` | Actual dial attempt, only when safely observable. |
| `transport_ready` | Passively observed readiness, not ClientConn creation or the whole handshake. |
| `rpc` | RPC invocation through completion. |
| `rpc_headers` | First received headers. |
| `rpc_first_message` | First received RPC message. |
| `worker_receive` | Actual worker request receive. |
| `worker_preparation` | Worker preparation. |
| `worker_submit` | Task submission. |
| `worker_write` | First response write attempt through completion, not client delivery. |
| `frontend_message` | First successful application `PredictStream.Recv`, before the reply callback. |
| `frontend_callback` | First token callback invocation, including prefixes and empty chunks. |
| `sse_role` | First serialized delta with a nonempty role, before its write attempt. |
| `sse_content` | First serialized delta with nonempty content, before its write attempt. |
| `sse_reasoning` | First serialized delta with nonempty reasoning, before its write attempt. |
| `sse_flush` | First existing HTTP flush call, from invocation to return. |
| `dispatch_summary` | Cumulative collection drops, emissions, and measured sink time. |

A nonempty metadata response does not imply marker presence. For
example, `SupportsThinking: true` with an empty media marker emits both
`metadata_nonempty` and `metadata_marker_absent`. These observations describe
each successful probe, not cached completion or changes to configuration.
Neither observation is emitted on a failed probe. Marker strings and unrelated
metadata content are never included in diagnostic events. Protobuf-size checks
are skipped when diagnostics are disabled; probe counts and behavior are unchanged.

Metadata decisions and routing/tracking operations classify both context
cancellation/deadline errors and gRPC `Canceled`/`DeadlineExceeded` status errors
(including wrapped errors) as `canceled`. Other failures remain `error`.
This classification only changes diagnostic outcomes, not returned errors,
health decisions, retries, reservations, or timeouts.

### Frontend stream boundaries

`frontend_message` is separate from `rpc_first_message`, which observes the
first gRPC stats `InPayload`. The application milestone follows a successful
`Recv`; EOF and receive errors do not produce it. No receive, warmup, or flush
is added. A reply prefix can invoke `frontend_callback` before the first
received message. Callback observations do not include token text or usage.

The SSE milestones describe write attempts, not successful delivery. Role
chunks and tool arguments do not count as content. A delta can contain both
role and content. Content and reasoning have independent first milestones.
The handler observes streamed, deferred-tool, and automatic-fallback deltas
at the same write boundary. It observes each milestone once across inference
iterations. Cloud-proxy passthrough does not use these local-stream boundaries.

`sse_flush` has start and end events around the first existing flush, including
an error-only or terminal flush. Echo's flush API returns no error. An `ok`
end means the call returned, not that a remote client received the data.
A write failure before any flush produces no flush milestone.

The HTTP handler and each streaming worker request separate completion drains.
HTTP unwind does not prove that detached callbacks have finished. A worker
requests its drain only after its actual producer exits, including cancellation
and write-failure paths. Instrumentation does not unblock, wait for, or cancel
producers; existing channel and cancellation behavior remains unchanged.

`grpc.FlushDiagnosticsAsync` shares the gRPC limit of 16 asynchronous owners
per process, with no waiting queue. Admission happens before goroutine creation.
Saturated submissions return immediately and increment `DiagnosticFlushDrops`.
This counter includes each rejected frontend completion submission, not unique
requests: handler and producer exits can count separately. Disabled requests
consume no slot. Admitted drains call the sink outside application and stats
locks. A stalled sink can retain an admitted owner indefinitely, but cannot
create an unbounded number of owners. Later completion submissions can recover
after capacity returns. Summaries can precede final producer completion;
missing events after drops do not establish absent work.
