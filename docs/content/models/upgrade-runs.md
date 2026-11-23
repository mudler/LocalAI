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

## Where the runs are kept

In a cluster the runs are rows in the shared database. On a single server
they are in the authentication database when authentication is on, and
otherwise in the SQLite file `upgrade_runs.db` under the data path. A server
with neither does not keep runs: the run routes answer `503`, and the single
upgrade routes below queue their upgrade directly, as before.

## Add-on items

An add-on item goes through the same path as
`POST /api/addons/{id}/upgrade`. The add-on owner records the upgrade for the
administrator who started the run, with the installed digest and the candidate
digest pinned, and checks it as it checks the explicit upgrade. The operation
queue then runs it for that administrator, and the owner checks the pins again
when it starts. A run never upgrades an add-on as a system identity.

The explicit upgrade is accepted only from the administrator's own browser
session. A run keeps that rule: its add-on items run only when the request
that started the run came from that session, with the header
`X-LocalAI-Confirm: upgrade-run`. Otherwise each add-on item ends `skipped`:

| Error key | Why |
|-----------|-----|
| `not_human_surface` | The run was started with an API key, by the assistant, or without the confirmation header. |
| `forbidden` | The account that started the run is not an add-on administrator. |

A refusal of the owner ends the item `failed` with the type of the owner, for
example `stale_installed`, `candidate_changed`, `incompatible` or
`upgrade_conflict`. An upgrade that needs host access nobody approved fails
with the failure code of the owner (`approval_required`); approve it on the
add-on page. A retry of a run with add-on items needs the same browser
session and header.

The list of add-ons with an update comes from the add-on owner. In a cluster,
only the frontend that is the add-on owner lists add-ons, so a preview or a
start of an add-on item on another frontend does not find it.

## REST API

All routes need an administrator. Their definitions are in the API reference
(Swagger, tag `updates`). A refusal has the same shape on every route:

```json
{"code": "run_active", "key": "", "message": "another upgrade run is running (2f0c...)"}
```

| Code | Status | Meaning |
|------|--------|---------|
| `invalid` | `400` | The request is malformed: no item, an unknown kind, an empty name, a source other than `manual`. |
| `not_found` | `404` | No such run, or no such item in the run. |
| `no_update` | `409` | The item is not installed, has no update, or cannot be updated on this server. |
| `stale` | `409` | The update no longer goes to `expected_to`. Preview again. |
| `source_unchecked` | `409` | The check of the kind of the item did not answer. Check again. |
| `run_active` | `409` | Another run is running. |
| `not_cancellable` | `409` | The item cannot stop now. |
| `run_finished` | `409` | The run is over. |
| `run_not_finished` | `409` | The run still runs (retry and dismiss). |
| `nothing_to_retry` | `409` | No item of the run can be retried, or none still has an update. |
| `not_human_surface` | `409` | A retry that includes add-on items did not come from the administrator's browser session. |
| `unavailable` | `503` | The server keeps no runs. |

`key` names the item when the refusal is about one.

| Route | Answer |
|-------|--------|
| `POST /api/updates/runs/preview` | `200`, the plan |
| `POST /api/updates/runs` | `202`, the run |
| `GET /api/updates/runs` | `200`, `{"runs": [...]}`, the running run first, then the newest. `?active=1` lists only the running run; `?limit=` caps the list (default 20). Dismissed runs are left out. |
| `GET /api/updates/runs/{id}` | `200`, the run |
| `POST /api/updates/runs/{id}/cancel` | `200`, the run |
| `POST /api/updates/runs/{id}/items/{key}/cancel` | `200`, the run. Encode the key: `library:main%2Fwhisper`. |
| `POST /api/updates/runs/{id}/retry` | `200`, the new run |
| `DELETE /api/updates/runs/{id}` | `204` |

A cancel answers at once; the runner applies it, so the item changes phase a
moment later.

Preview. The body is optional; an empty list previews every item with an
update:

```bash
curl -X POST http://localhost:8080/api/updates/runs/preview \
  -H "Content-Type: application/json" \
  -d '{"items": [{"kind": "gallery", "name": "kokoro"}, {"kind": "model", "name": "qwen3-8b"}]}'
```

```json
{
  "items": [
    {"key": "gallery:kokoro", "kind": "gallery", "name": "kokoro", "from": "v1.2.0", "to": "v1.3.0",
     "expected_to": "v1.3.0", "stage": 1, "order": 0, "startable": true,
     "risks": ["stops_models"], "selected": true,
     "default_selected": true, "risk": [{"code": "stops_models", "params": {}}]},
    {"key": "model:qwen3-8b", "kind": "model", "name": "qwen3-8b", "from": "sha256:aa", "to": "sha256:bb",
     "expected_to": "sha256:bb", "stage": 2, "order": 1, "startable": true,
     "risks": ["stops_model", "downloads"], "selected": false,
     "default_selected": false, "risk": [{"code": "stops_model", "params": {}}, {"code": "downloads", "params": {}}]}
  ],
  "order": ["gallery:kokoro", "model:qwen3-8b"],
  "complete": true
}
```

`reason` says why an item cannot start: `held`, `edited_keys`, `in_run`,
`no_update` or a reason of the server such as `installs_disabled`.
`active_run_id` is set while a run runs. `complete` is `false` when a source
did not answer, so items may be missing.

Start. Send back the `expected_to` of each item:

```bash
curl -X POST http://localhost:8080/api/updates/runs \
  -H "Content-Type: application/json" \
  -d '{"items": [{"kind": "gallery", "name": "kokoro", "expected_to": "v1.3.0"}], "source": "manual"}'
```

```json
{
  "id": "2f0c6a52-0f7e-4b5e-9a3e-0c8a1f7f3c11",
  "source": "manual",
  "actor": "admin-user-id",
  "created_at": "2026-11-23T10:00:00Z",
  "state": "running",
  "items": [
    {"key": "gallery:kokoro", "kind": "gallery", "name": "kokoro", "from": "v1.2.0", "to": "v1.3.0",
     "expected_to": "v1.3.0", "phase": "running", "progress": 12, "message": "downloading",
     "retryable": false, "cancellable": true, "job_id": "8d1e..."}
  ]
}
```

`job_id` is the operation of `/api/operations` that runs the item. `actor` is
the id of the account that started the run.

## The single upgrade routes

These routes keep their answers for existing clients. Each starts a run of one
item and adds the run to its answer:

| Route | Answer | Added |
|-------|--------|-------|
| `POST /api/backends/upgrade/{name}` | `200`, `jobID`, `uuid`, `statusUrl`, `message` | `run` |
| `POST /backends/upgrade/{name}` | `200`, `id`, `status_url` | `run` |
| `POST /api/models/upgrade/{name}` | `200`, `id`, `jobID`, `status_url`, `message` | `run` |
| `POST /api/addons/{id}/upgrade` | `200`, the operation status with `jobId` | `run` in the payload |

While another run runs, they answer `409` with code `run_active` (the add-on
route in its error type). The backend routes take an optional `expected_to`, in
the query or the JSON body, and answer `409` `stale` when it moved. When the
item cannot start, they answer `409` with the error key of the item as `code`.
When the check of the run does not list the item (it was not checked yet, or
an older listing), or the server keeps no runs, they queue the upgrade
directly, as before. A model upgrade with `config_only` or `choices` is always
direct: a run makes no such choice. The per-node route
`POST /api/nodes/{id}/backends/upgrade` does not start a run.

## From the assistant

The [LocalAI Assistant]({{% relref "/features/chats-and-agents/localai-assistant" %}})
starts, reads and cancels runs of backends and models:

- `upgrade_run_start` takes `items`, each `{kind, name, expected_to}` with kind
  `gallery`, `library` or `model`. It refuses add-ons: their upgrade needs your
  own browser session.
- `upgrade_run_status` reads a run by `id`, or the running and recent runs.
- `upgrade_run_cancel` cancels a run, or one item with `key`.

The assistant asks you to confirm before it starts or cancels a run.
`upgrade_backend` and `upgrade_model` still upgrade one item; they start a run
of one item too.
