# Distributed Staging Operations Design

## Problem

`GET /api/operations` reads file-transfer progress from the frontend replica's
in-memory `StagingTracker`. Distributed frontends broadcast tracker updates over
NATS, but those messages are transient. A replica that starts after staging has
begun, temporarily disconnects, or misses an update can return no staging row.
When a browser's one-second polls are balanced across replicas, the operation
therefore appears and disappears.

Distributed cold loads already persist their phase, placement, heartbeat, and
byte progress in PostgreSQL's `model_load_jobs` table. That row is the durable
cluster authority and should provide the baseline operations view.

## Design

Add a `NodeRegistry` query that lists active model-load jobs. The operations
endpoint will use those jobs to build one staging operation per tracking key
when the job is in the `staging` phase. It will then overlay matching local or
NATS-mirrored `StagingTracker` data, because the tracker can contain a fresher
message and filename than the periodically persisted job.

The merge is keyed by the model tracking key. A tracker entry replaces the
database entry's progress and display details rather than creating a duplicate.
Tracker-only entries remain visible for compatibility with staging paths that
do not have a durable load-job row. Database-only entries remain visible on
every replica, which eliminates flicker.

The database row supplies:

- stable operation identity (`staging:<tracking key>`),
- model name and staging phase,
- node name,
- overall progress calculated by `ModelLoadJob.Progress()`, and
- byte counters used by the frontend's ETA calculation.

The tracker overlay supplies its message, filename, node name, progress, and
byte counters when available.

## Failure Handling

If the database query fails, `/api/operations` will log the error and fall back
to the current tracker-only response. An observability failure must not break
the entire operations endpoint or hide unrelated gallery operations.

Only live `staging` rows are included. Pending, backend-installing, loading, and
failed rows are represented by their existing user-facing flows and must not be
mislabelled as file staging.

## Testing

Add focused Ginkgo coverage for:

1. A database-only staging job appears in the operations payload, reproducing
   the request landing on a replica that missed all NATS broadcasts.
2. A matching tracker entry overlays the database entry without duplication.
3. Non-staging load jobs do not appear as staging operations.
4. A database read failure retains tracker-only staging operations and the
   endpoint still succeeds.

Run the affected Go package tests only; no long build is required.

## Documentation

This corrects consistency of an existing UI operation and introduces no new
API, option, or user workflow. No user documentation change is required.
