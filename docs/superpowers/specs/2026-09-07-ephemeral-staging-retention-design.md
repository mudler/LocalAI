# Ephemeral staging retention

## Problem

Distributed requests copy transient inputs to a worker below
`<staging>/ephemeral/<category>/<request-id>`. LocalAI already has a worker
cleanup loop, but its six-hour retention and 30-minute sweep defaults are too
large for high-frequency inputs. A Reachy Mini sending camera and sound data
about once per second created more than 21,000 request directories and filled
the Mac worker before the first entries became eligible for deletion.

The current cleanup also decides whether a request directory is stale from the
directory's own modification time. Creating a payload updates that timestamp,
but continuing to write the payload does not. A sufficiently long upload can
therefore look stale while its payload is still changing.

## Goals

- Bound worker-local ephemeral retention to one hour, matching LocalAI's
  existing object-storage retention default.
- Sweep immediately at worker startup and every 15 minutes afterward.
- Preserve a request directory when any file within it is newer than the
  retention cutoff.
- Apply the policy to every ephemeral category and to both HTTP and S3/NATS
  worker-local staging.
- Never inspect or remove models, data, configuration, or paths outside the
  worker's `staging/ephemeral` tree.
- Keep cleanup best-effort: an unreadable or undeletable entry is logged and
  does not stop the worker or the rest of the sweep.

## Non-goals

- Immediate deletion at the end of each RPC.
- A byte quota that can evict inputs belonging to long-running requests.
- Cleanup of model files or other persistent worker data.
- Deployment configuration changes for individual workers.

## Design

Keep `StartEphemeralStagingCleanup` as the lifecycle owner. A zero or negative
TTL selects one hour, and a zero or negative interval selects 15 minutes. The
cleanup goroutine performs one sweep when it starts, repeats on the interval,
and exits when the worker shutdown context is cancelled.

`CleanEphemeralStaging` continues to enumerate only the category and request
levels below `<staging>/ephemeral`. Before deleting a request entry, it computes
that entry's newest modification time, including descendants. If any payload or
upload sidecar has been modified at or after the cutoff, the entire request is
kept. Directory symlinks are not followed. A symlink encountered as a request
entry may be unlinked, but cleanup must never traverse through it.

The default one-hour TTL plus the 15-minute interval gives completed or
abandoned requests a maximum normal residence of about 75 minutes. The startup
sweep handles files left by a prior worker crash without waiting for the first
timer tick.

## Error handling and observability

Missing ephemeral roots are normal and produce no warning. Read, stat, and
remove failures include the affected path in a warning and allow the sweep to
continue. A successful sweep logs the number of request entries removed. The
startup log reports the effective TTL and interval.

## Testing

Regression tests will establish the following behavior:

1. With default settings, a request two hours old is removed by the startup
   sweep. This fails with the current six-hour default.
2. A request directory older than the cutoff is retained when a nested payload
   was modified recently. This fails with the current directory-only age test.
3. A request whose directory and descendants are stale is removed.
4. Fresh requests, persistent paths outside `ephemeral`, and symlink targets
   outside the staging tree remain untouched.
5. Cancellation stops the periodic cleanup loop.

Focused worker tests will run with race detection after the red/green cycle,
followed by the repository's relevant lint and vet checks.

## Rollout

The change requires a new LocalAI worker build. On startup, the Mac worker will
remove the backlog older than one hour using its existing root privileges. No
manual deletion or model removal is required. After deployment, verification
will check available disk space, the cleanup log, and that new sound, vision,
and transcription requests still succeed.
