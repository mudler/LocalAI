# Distributed Model Configuration Revisions

## Problem

Editing a model configuration in a distributed LocalAI deployment can leave the
cluster serving different effective configurations for the same logical model.
The frontend reloads the edited YAML and asks workers to stop the model, but the
existing `backend.stop` message is fire-and-forget. The frontend therefore
removes routing state without knowing whether the worker process stopped.

Separately, the replica reconciler persists `ModelLoadInfo` independently of
live `NodeModel` rows. This is necessary for restoring `min_replicas` after a
worker failure, but the persisted options currently have no relationship to a
specific revision of the model configuration. After an edit, the reconciler can
restore a replica from options captured before the edit.

The observed result was one replica serving a context near 100K while another
served the default 8K context and default parallelism. Requests behaved
differently depending on which replica the router selected. The problem is not
specific to `context_size`: any load-time model option can be stale.

## Goals

- Make all routable replicas of a logical model belong to the current model
  configuration revision.
- Prevent the reconciler and late load jobs from restoring options belonging to
  an older revision.
- Remove a model from routing before attempting distributed cleanup.
- Confirm that the exact worker process exited before deleting its registry
  row.
- Recover safely when a worker or NATS is temporarily unreachable.
- Apply the same lifecycle to raw YAML edits, structured configuration patches,
  renames, disabling, and changes received from peer frontends.
- Preserve the existing ability to restore `min_replicas` after ordinary
  worker or backend failure when the model configuration has not changed.
- Expose enough state to diagnose why two replicas have different effective
  options.

## Non-goals

- Requiring identical hardware-derived options on heterogeneous workers.
- Changing `model.unload`, which remains a memory-release operation.
- Replacing backend administration operations such as backend upgrade, delete,
  or stop-all.
- Automatically upgrading workers that do not support the new stop protocol.
- Making arbitrary out-of-band filesystem edits transactional across multiple
  machines. Such edits are detected when the model configuration loader next
  refreshes the model.

## Configuration identity

Each validated model configuration has a `config_revision`. The revision is a
SHA-256 digest of a canonical semantic representation of the validated model
configuration. Formatting, YAML comments, and map ordering do not affect the
revision. Load-time request overrides and node-specific hardware tuning are not
part of this digest.

Canonicalization must use the typed, validated configuration rather than raw
YAML bytes. The canonical representation includes every field that can affect
model loading or serving. Fields used only to locate the source file or report
runtime status are excluded. The canonical encoder must produce stable field
and map ordering and must distinguish absent values where absence has different
semantics from an explicit zero value.

The revision is carried with the model options from configuration loading into
the distributed router. It is also persisted in:

- `ModelConfigState`, keyed by logical model name, as the currently accepted
  revision;
- `ModelLoadInfo`, alongside the serialized `pb.ModelOptions` used for future
  reconciliation;
- `NodeModel`, identifying the revision used for that live replica.

Each `NodeModel` also records an `effective_options_hash`, computed from the
fully materialized `pb.ModelOptions` after node-specific hardware defaults and
file-path staging rewrites. This hash is diagnostic only. Two replicas may have
different effective hashes and remain compatible when they share the same
configuration revision.

Rows created by older versions have an empty revision. They remain usable until
the model's first revision-aware configuration mutation. Once a current
revision is recorded, empty-revision rows are stale and cannot be routed.

## Registry invariants

The database is the coordination boundary shared by frontend replicas.

1. At most one current configuration revision exists per logical model name.
2. A `NodeModel` is routable only when it is in the loaded state and its
   `config_revision` equals the current `ModelConfigState` revision.
3. A `ModelLoadInfo` row is reconcilable only when its revision equals the
   current `ModelConfigState` revision.
4. A load job may publish `NodeModel` or `ModelLoadInfo` state only when its
   captured revision still equals the current revision.
5. Advancing the current revision and quarantining prior-revision replica rows
   happen in one database transaction.

The load-info upsert becomes compare-and-set rather than unconditional
last-write-wins. If the load's revision is no longer current, the upsert returns
a typed stale-revision error. The load is then abandoned and its worker process
is stopped through the exact stop protocol. A late load can therefore neither
be routed nor overwrite current reconciliation options.

Normal worker death does not change `ModelConfigState` or delete matching
`ModelLoadInfo`; this preserves restart recovery. A configuration mutation
advances `ModelConfigState` and invalidates older load information.

## Configuration mutation lifecycle

All model configuration mutation entry points use one model administration
lifecycle service. The structured PATCH endpoint must no longer bypass local
shutdown behavior.

For an edit that keeps the same logical model name, the service:

1. Validates and persists the new configuration.
2. Reloads it and computes its semantic revision.
3. In one transaction, records the new current revision, marks every replica
   from another or empty revision as `unloading`, and removes or supersedes old
   `ModelLoadInfo`.
4. Broadcasts the revision-aware invalidation to peer frontends.
5. Starts cleanup for each quarantined replica using exact `model.stop`.
6. Deletes a replica row only after confirmed process termination or confirmed
   absence of that exact process.

Marking rows `unloading` precedes network calls. A worker that cannot be reached
therefore cannot continue receiving inference traffic through LocalAI even if
its old backend process is still alive.

The configuration save is durable even if cleanup is incomplete. The endpoint
must not report that saving failed after the new file and revision have
committed. Its response reports that cleanup is pending, and the condition is
also logged and exposed through the existing model/node lifecycle status
surfaces. Subsequent retries finish cleanup.

For rename, the old identity is quarantined and stopped under its old name. The
new identity receives its own current revision. Old load information is not
copied to the new name. Disable performs the same quarantine and cleanup but
does not permit fresh loads while disabled. Delete follows the existing file
deletion lifecycle after exact process cleanup.

Peer invalidation events carry the logical model name, operation, and new
revision. Applying an event is idempotent. A peer that already observes that
revision refreshes its in-memory configuration but does not create a second
cleanup generation.

When the existing configuration watcher detects an out-of-band file change, it
computes the revision after validation and submits the same lifecycle
transition. A parse or validation failure leaves the last accepted revision
current and does not quarantine its replicas. This does not make filesystem
writes atomic, but it ensures a successfully observed external edit cannot
silently bypass revision-aware routing.

## Exact worker process stop

A new request/reply NATS operation, `model.stop`, is separate from the existing
ambiguous `backend.stop` operation.

The request contains:

```text
model_name
process_key
expected_address
force
config_revision
```

`process_key` is the exact supervisor key, including replica index. The
controller derives it from the registry row rather than asking the worker to
resolve a bare backend or model name. `expected_address` prevents a stale row
from stopping an unrelated process after port reuse. `config_revision` is
included for auditability; process key and expected address are the worker-side
identity checks because workers do not own the configuration database.

The reply contains:

```text
matched
freed
terminated
process_key
address
error
```

The worker verifies that both process key and address identify the same
supervised process. A mismatched address is an error and never stops anything.
An absent process is a successful idempotent outcome with `matched=false` and
`terminated=true` because there is no process left to clean up.

For a graceful request, the worker performs bounded gRPC `Free()` and then
terminates the supervised process. A `Free()` failure is recorded but does not
prevent termination. A forced request skips `Free()`. The worker replies only
after the process has exited and its supervisor bookkeeping and port ownership
have been updated.

The existing operations retain their meanings:

- `model.unload` calls gRPC `Free()` without promising process termination;
- `backend.stop` remains an administration and compatibility operation whose
  identifier may be a backend name;
- `model.stop` is the only operation used to confirm configuration-generation
  cleanup for an exact replica.

Sending both `model.unload` and `model.stop` is unnecessary because graceful
`model.stop` already performs bounded `Free()` before termination.

## Unreachable workers and retry

An `unloading` replica is never routable. Failed `model.stop` attempts retain
the row with its last error, attempt count, and next retry time. A bounded,
backoff-based cleanup loop retries exact stops. Retries are idempotent and are
claimed through the database so multiple frontend replicas do not concurrently
own the same attempt.

The existing recovery paths remain backstops:

- Worker re-registration clears all `NodeModel` rows for that node because a
  restarted worker has no surviving supervised backend processes.
- The per-model health monitor removes rows after consecutive unreachable
  backend probes.
- Node offline handling prevents scheduling onto a worker with stale
  heartbeats.

Cleanup-row removal through any of these paths fires the existing replica
removal hooks. It does not restore stale `ModelLoadInfo` because only the
current revision is eligible for reconciliation.

If a worker keeps heartbeating but does not support `model.stop`, the row stays
quarantined and the error clearly identifies an incompatible worker version.
The system favors temporary unavailability over silently serving an obsolete
configuration. Restarting or upgrading that worker lets re-registration or a
subsequent retry complete cleanup.

## Reconciliation and loading

The reconciler reads the current revision and matching `ModelLoadInfo` in one
consistent operation. If no matching load information exists, it does not use
an older blob. It records a diagnostic explaining that the model must first be
loaded under its current revision.

The next inference request builds options from the current configuration,
captures its revision, and performs the normal install, staging, and load
sequence. On success, it transactionally records the replica and current
`ModelLoadInfo`. The reconciler may then restore additional `min_replicas`
using that revision.

Every scheduling and routing decision rechecks revision eligibility when it
claims a replica. A replica selected immediately before a concurrent edit must
fail the claim after the edit advances the current revision. Existing in-flight
requests may finish; no new request is assigned to the old replica. Graceful
cleanup waits for bounded `Free()` behavior and then terminates it.

## API and observability

Model and node lifecycle responses should expose, where replica details are
already returned:

- current model `config_revision`;
- replica `config_revision`;
- `effective_options_hash`;
- lifecycle state, including `unloading`;
- pending cleanup error and retry time.

Logs for routing, reconciliation, load completion, stale-load rejection, and
cleanup include model name, replica index, node ID, and abbreviated revision.
No serialized model options or request content is added to logs.

The Web UI does not require a new workflow. After saving, it may show that the
configuration is saved while one or more old replicas are still being cleaned
up. User-facing distributed-model documentation explains this state and the
requirement to upgrade workers that lack acknowledged `model.stop` support.

## Rolling upgrades

Database migrations add nullable revision and cleanup columns so old binaries
can continue reading existing rows. New frontends treat missing revisions as
legacy state according to the compatibility rule above.

The new NATS subject avoids changing the semantics of `backend.stop` for old
workers. A new frontend receiving no responder for `model.stop` leaves the
replica quarantined and reports the compatibility problem. It must not fall
back to fire-and-forget `backend.stop`, because doing so would recreate the
original false-success failure.

Deployments should upgrade workers before or together with frontends. Mixed
frontend versions are tolerated at the database level, but old frontends do
not enforce revision-aware routing. Documentation must state that strict
cross-replica consistency is guaranteed only after all frontend replicas run
the revision-aware version.

## Testing

All Go tests use Ginkgo and Gomega.

### Registry tests

- Advancing a revision and quarantining old replicas is atomic.
- Only loaded replicas matching the current revision are returned for routing.
- Empty legacy revisions become stale after a revision-aware mutation.
- Load-info compare-and-set rejects a late old-revision write.
- Matching load information survives ordinary replica removal and worker
  failure.
- Re-registration removes quarantined rows without changing current revision or
  matching load information.

### Router and reconciler tests

- Given one 8K old-revision replica and one 100K current-revision replica, every
  new request routes to the current revision.
- Changing `parallel` produces the same revision transition behavior as changing
  `context_size`.
- The reconciler never loads from stale `ModelLoadInfo`.
- A late durable load job cannot publish a stale replica or overwrite current
  load information.
- A request racing a configuration edit cannot claim the old generation.
- Heterogeneous effective option hashes remain routable when their
  configuration revision matches.

### Worker protocol tests

- Exact process key and address stop the intended process and wait for exit.
- An address mismatch stops nothing.
- An already-absent process returns idempotent success.
- Graceful stop attempts bounded `Free()` and still terminates after a failure.
- Forced stop skips `Free()`.
- Replica port ownership and quarantine are updated before replying.

### Lifecycle tests

- Raw YAML edit, structured PATCH, rename, disable, and peer application all
  advance or apply the expected revision and quarantine old replicas.
- A successful stop deletes the matching row.
- A timeout leaves a non-routable `unloading` row with retry state.
- Retry eventually deletes the row after the worker recovers.
- A worker without `model.stop` support produces a visible compatibility error
  and never triggers fire-and-forget fallback.
- Partial cleanup does not roll back an already persisted configuration edit.

### Live distributed regression

An integration scenario loads a model on two workers, edits context and
parallel settings, and verifies that no request is routed to an old revision.
After cleanup and reload, every replica reports the current revision. The test
also disconnects one worker during the edit, verifies its replica is
quarantined, reconnects it, and verifies retry or re-registration removes the
stale row.

## Documentation impact

The implementation updates the distributed model lifecycle documentation under
`docs/content/` in the same change. It documents revision consistency,
quarantined cleanup state, rolling-upgrade requirements, and why an edited model
may wait for its first request before `min_replicas` can be restored.

No configuration key or public inference API changes are introduced.
