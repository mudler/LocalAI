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

With `--request-phase-timing` (`LOCALAI_REQUEST_PHASE_TIMING=true`), requests that reach model extraction receive `X-LocalAI-Diagnostic-ID`.
The header contains the same server-generated UUID v4 as that request's phase events.
LocalAI sets it before extraction errors or streaming output can write the response.
Repeated extraction and failover attempts retain one ID for the logical request.

Client-supplied diagnostic or correlation headers do not select this ID.
The ID contains no model name, authentication token, or request content.
It is a correlation value, not an authorization credential or a distributed trace ID.
Requests rejected before extraction, including global authentication failures, do not receive this header.
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
