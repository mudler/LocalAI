+++
disableToc = false
title = "Upgrade Runs"
description = "How the server runs the update of several backends, models and add-ons as one run, and how a run behaves in a cluster"
weight = 86
+++

An upgrade run updates a set of backends, gallery models and add-ons in one
request. The server owns the run: it orders the items, starts each update,
follows it to the end and keeps the result. A reload of the page, a restart of
the server or the loss of one frontend in a cluster does not lose the run.

This page describes how a run works. For the updates themselves, see
[Model Updates]({{% relref "/models/model-updates" %}}),
[Backends]({{% relref "/models/backends#the-updates-page" %}}) and
[Install and update add-ons]({{% relref "/addons/install-and-update" %}}).

## Items

Each item has a key `<kind>:<name>`, the same id the Updates page uses:

| Kind | Example | What it updates |
|------|---------|-----------------|
| `gallery` | `gallery:kokoro` | A backend from a backend gallery |
| `library` | `library:main/whisper` | A backend from a backend library (`<source>/<name>`) |
| `model` | `model:qwen3-8b` | A model installed from a model gallery |
| `addon` | `addon:notes` | An installed add-on |

## Starting a run

A preview lists the items that have an update, in run order, with what each
update does (for example, a backend update stops the models that use it) and
whether it can start. Backends and add-ons are selected by default. Models are
not, because a model upgrade stops the model.

A start request names the items and, for each one, the version or digest the
caller saw (`expected_to`). The server checks every item before it creates the
run: the item must be installed, it must have an update, and the update must
still go to `expected_to`. If one item fails a check, the server refuses the
whole request and creates no run.

A held model, or a model whose update changes configuration keys that a
person edited, is part of the run as `skipped`. A run makes no choice for
edited keys. Upgrade such a model from its own page.

Only one run runs at a time.

## Order

A run has three stages:

1. **Backends** (`gallery` and `library`). Up to the backend operation limit
   (`max_concurrent_backend_operations`, default 2) run at the same time. Two
   updates of the same backend never run at the same time.
2. **Models**, after all backends are done, one at a time.
3. **Add-ons**, after all models are done, one at a time. The add-on owner
   runs one lifecycle operation at a time.

A queued item tells why it waits (`queued_reason`): `after_backends`,
`after_models`, `backend_limit`, `same_backend`, `one_model_at_a_time` or
`one_addon_at_a_time`.

Each item uses the same operation as a single update: the backend operation
queue, the model upgrade, the library install or the add-on upgrade. The
operation shows in `/api/operations` as usual, and the progress of the item is
read from it.

## When an item is done

An item is `updated` only when both of these are true:

1. its operation ended without an error, and
2. a fresh check no longer lists the item.

If the operation ended but the check still lists the item, the item is
`failed` with the error key `stale`. If no check can answer (a source failed),
the item is `unconfirmed`.

| Phase | Meaning |
|-------|---------|
| `queued` | Waits for its stage or a free slot |
| `running` | Its operation runs, or the check runs |
| `updated` | Done, and the check confirmed it |
| `failed` | The operation failed, or the item is still listed |
| `rolled_back` | The operation failed and restored the previous version |
| `cancelled` | Cancelled before or while it ran |
| `skipped` | Held, edited keys, or no update any more |
| `unconfirmed` | No check could confirm the result |

A failure has an error key: `download`, `verify`, `swap`, `start`, `held`,
`edited_keys`, `stale` or `unknown`. A failed add-on keeps the failure code of
the add-on owner, for example `upgrade_failed` or `recovery_required`.

## Cancel, retry and dismiss

- Cancelling a queued item removes it from the run. A running item can be
  cancelled only while its operation says it can stop. For example, a model
  upgrade cannot stop while it switches the files.
- Cancelling the run cancels the queued items and stops the running ones when
  they can stop. The run then ends `cancelled`.
- Retry starts a new run with the items of a finished run that failed, rolled
  back or stayed unconfirmed, if they still have an update. The old run does
  not change. A run that is over changes only when it is dismissed.
- Dismiss hides a finished run from the default list.

## Restarts

The run and its items are stored in the database. When the server restarts,
it takes over the runs that were running. The operations of the old process
stopped with it, so each item that was running becomes `unconfirmed` and a
fresh check decides: `updated` if the item is no longer listed, `failed` with
`stale` if it is. Queued items continue.

## Distributed mode

The run uses two of the modes of distributed state: shared state and a single
runner.

- **Shared state.** The runs are rows in the shared database (`upgrade_runs`
  and `upgrade_run_items`), on PostgreSQL in a cluster and on SQLite on a
  single server. Every frontend can read a run, list the runs, cancel a run or
  an item, retry and dismiss. The progress of a running item is not copied
  into these rows: each frontend reads it from the status of the operation,
  which the frontends already share.
- **Single runner.** One frontend runs a run. It holds an advisory lock of
  that run for as long as it runs it, and only it starts operations, follows
  them and writes the state of the run. A cancel through another frontend sets
  a flag in the database, and the runner applies it. A broadcast on the bus
  makes the runner apply it at once, and tells the other frontends that the
  run changed.
- **Takeover.** When the frontend that runs a run dies, PostgreSQL frees its
  lock (in about 30 seconds). Every frontend looks for runs that nobody runs
  at a regular interval, and the first one that takes the lock continues the
  run as after a restart.
- **Admission.** A second advisory lock lets only one frontend at a time start
  a run, so two frontends cannot start two runs at the same time.
- **Stale operations.** The 30-minute reaper of gallery operations fails an
  operation that did not change for 30 minutes. An operation of a run can wait
  that long in the queue behind a large download. The runner updates the
  operations it follows at a regular interval, so the reaper does not fail
  them. When the runner dies, it stops doing that, and the reaper handles
  them as usual.
