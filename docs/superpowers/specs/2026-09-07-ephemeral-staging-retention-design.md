# Request-owned ephemeral staging

## Problem

Distributed requests copy transient inputs below
`<staging>/ephemeral/<category>/<request-id>`. The worker currently removes
these files only when a periodic age sweep considers them stale. A Reachy Mini
sending camera and sound data about once per second created more than 21,000
request directories and filled its Mac worker before the six-hour retention
window elapsed.

Reducing the retention window is insufficient. A time limit bounds residence
time, but the retained bytes still scale with request rate and input size. A
quota sweeper would also have to infer whether an old file is still in use.
Neither rule prevents concurrent uploads from consuming the worker's last free
space.

## Goals

- Give every ephemeral input an explicit owner and release it when that request
  finishes, fails, or is cancelled.
- Keep cleanup transport-independent for HTTP and S3/NATS workers.
- Reserve capacity before accepting ephemeral bytes so concurrent requests
  cannot consume configured disk headroom.
- Reject a request cleanly when its input does not fit; never evict an input
  that a running request may still be reading.
- Recover abandoned files after frontend or worker crashes.
- Never inspect or remove models, data, configuration, or paths outside the
  worker's ephemeral staging tree.

## Non-goals

- Retaining request inputs as a cache.
- Evicting persistent model or data files to make an inference request fit.
- Treating modification timestamps as proof that a request is active.

## Request ownership

The `FileStagingClient` already creates one request ID before staging inputs and
waits for synchronous and streaming backend calls to finish. It will track each
ephemeral key before attempting to stage it and defer one request-scoped
release identified by the request ID. Release runs after the backend call
returns, including error and cancellation paths, using a short background
timeout so cancellation of the request does not cancel its cleanup.

Request IDs will use the full UUID rather than the current eight-character
prefix. The worker enumerates only category directories for that validated
request ID and removes each entry with exact, symlink-safe deletion.

`FileStager` will expose an idempotent exact-key `ReleaseRemote` operation and
an optional request-scoped operation. The client uses one fixed-size request
message for the normal path and retains exact-key calls as a rolling-upgrade
fallback:

- HTTP sends one authenticated request containing the fixed-size request ID.
  The worker derives and removes that request's exact files, then prunes empty
  request and category directories without following symlinks.
- S3/NATS sends one request-reply containing the request ID so the selected
  worker evicts the request's local cached files. The frontend then deletes the
  matching objects from its tracked exact-key list.
  Either deletion may already have happened and still counts as success.

If staging fails partway through a request, the deferred release still includes
the planned key, allowing it to remove a partial file when the transport can
identify one. Cleanup errors are logged and do not replace the inference result.

HTTP and S3 ingress register request operations before any pre-reservation
work. Before enumerating files, the capacity guard marks the request released
and waits for registered operations and admitted writes to finish. Later
operations, reservations, and cache claims for that request are rejected.
Markers expire after one hour and are capped at 16,384 entries, but a marker is
never evicted while its registered operation or cleanup scan is active.
Concurrent operation and cleanup state have the same hard cap. Disk bytes
remain independently bounded by capacity admission.
Cleanup waits within its deadline when all cleanup-pin slots are occupied.
If that deadline expires, existing entries lose active ownership so recovery
can reclaim them; registered ingress for the request remains closed until it
exits.

## Capacity admission

A worker-local ephemeral capacity guard is shared by its HTTP and S3/NATS input
paths. It accounts for both `<staging>/ephemeral`, used by HTTP, and
`<cache>/ephemeral`, used by S3 downloads. Before writing an ephemeral object,
the transport reserves its declared size. HTTP obtains the size from the upload
metadata; S3/NATS obtains it from object metadata. Reservations are serialized
in memory, cover both committed ephemeral bytes and concurrent writes, and are
returned on release or failed transfer.

Admission succeeds only when both conditions remain true after the reservation:

1. Total ephemeral bytes remain below the configured ephemeral staging limit.
2. The filesystem retains the configured minimum free-space headroom.

The guard rejects the transfer before inference when either condition fails.
An input with unknown size is written through a bounded accounting writer that
reserves fixed-size chunks before writing each chunk and stops before crossing
the limit. The existing maximum-upload-size check remains the per-file ceiling.

The limit and headroom are worker settings. By default, ephemeral data may use
the smaller of 10 GiB or 10 percent of filesystem capacity, while the worker
preserves the larger of 1 GiB or 5 percent as free-space headroom. The worker
logs the effective values at startup. A zero or negative operator value selects
the default rather than disabling protection. The guard scans the ephemeral
tree at startup to account for abandoned committed bytes. Filesystem free-space
checks are repeated at reservation time because other processes may share the
volume.

## Crash recovery

The existing periodic cleanup remains as a fallback for ownership messages lost
when a frontend or worker process dies. It uses a one-hour recovery TTL,
performs one startup sweep, and repeats every 15 minutes. It skips every key
held by an active reservation, considers the newest modification time in each
remaining request tree, and does not follow directory symlinks. It removes only
request directories below the registered `<staging>/ephemeral` and
`<cache>/ephemeral` roots.

The recovery window does not control normal storage growth. Request completion
and capacity reservations do. A recovery deletion updates the capacity guard's
accounted bytes.

## Error handling and observability

Admission failures report the requested bytes, current ephemeral usage, limit,
available bytes, and required headroom. Successful release and recovery update
usage counters. Read, stat, and remove failures include the affected path and
allow unrelated cleanup to continue. Missing ephemeral files and directories
are normal for idempotent release.

## Testing

Regression tests will establish the following behavior:

1. Successful, failed, cancelled, and streaming calls issue one request-scoped
   worker cleanup only after the backend has returned.
2. Partial staging failures release the planned key without changing the main
   error returned to the caller.
3. HTTP and S3/NATS release remove local files; S3/NATS also removes the object.
4. Release rejects persistent keys and path traversal, does not follow
   symlinks, and leaves paths outside `ephemeral` untouched.
5. Concurrent reservations cannot exceed the byte limit or free-space
   headroom, and failed transfers return their reservations.
6. Unknown-length writes stop at the capacity boundary.
7. Startup accounting includes abandoned ephemeral files, and the recovery
   sweep removes only stale, inactive leftovers and updates accounting.

Focused package tests will run with race detection, followed by the relevant
repository lint and vet checks.

## Rollout

The change requires a new LocalAI worker and frontend build because both sides
participate in release. The Mac worker starts by accounting for its existing
backlog and removing recovery-expired files. The deployment check will verify
available space, admission and release logs, stable ephemeral usage under
continuous camera and audio traffic, and successful vision, sound detection,
and transcription requests.
