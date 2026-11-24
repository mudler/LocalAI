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

A start request names the items and, for each one, the `expected_to` of the
preview. The server checks every item before it creates the run: the item must
be installed, it must have an update, and the update must still go to
`expected_to`. If one item fails a check, the server refuses the whole request
and creates no run.

### What `expected_to` guarantees

`expected_to` is checked, not enforced inside the operation:

- **Before each item starts.** Just before the operation of an item starts,
  the run checks the item again. When the update no longer goes to
  `expected_to`, the item ends `failed` with the error key `stale`
  (retryable), and nothing is installed.
- **During the operation.** A gallery backend or model operation reads the
  gallery when it runs and installs the build the gallery lists then. It does
  not compare that build with `expected_to` again, so a gallery change in the
  short time between the check and the download is installed. A library
  backend item passes the version it was checked for to the library install. The integrity
  checks of the gallery entry (the SHA-256 and the signature policy) run on
  the downloaded build before the swap; a build that fails them is never put
  in place.
- **Add-on items** are pinned: the add-on owner records the upgrade with the
  installed digest and the candidate digest (`expected_to`) when the item is
  queued, and checks both again when the operation runs. A candidate that
  moved ends the item with the owner's code, for example `candidate_changed`.

`from` and `to` are for people: the version, or the digest when there is no
version. `expected_to` is what the request pins. For a backend and a model it
is the same as `to`. For an add-on, `from` and `to` are versions and
`expected_to` is the digest of the candidate release, because a version tag can
move to other content. An add-on item also has `title`, the name the Add-ons
page shows.

### Risks

A preview item lists what its update does beyond replacing the item, as
`risks` (the codes) and `risk` (each code with its numbers). The numbers are
decimal strings. A risk is listed only when the server knows it:

| Code | Params | Meaning |
|------|--------|---------|
| `stops_models` | `count`, `nodes` | `count` loaded models use the backend on `nodes` nodes. They stop while it is replaced. |
| `cluster` | `nodes` | The backend is replaced on `nodes` nodes. Sent when no loaded model is known to use it. |
| `drift` | `count` | `count` nodes run a different build of the backend now. The update moves every node to the new build. |
| `config_only` | | The model update changes only its configuration. Nothing is downloaded. |
| `migrates_data` | | An add-on update. The owner backs up the data of the add-on first, and the new version can migrate it. |

Only a cluster knows which models run with a backend, so a single server does
not send `stops_models`, `cluster` or `drift`. Every model update stops the
model, downloads the changed files and loads the new version as a check; this
has no risk code.

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

While the check runs, the item has `verifying: true`: it is still in
progress, also in the `unconfirmed` phase after a restart. An `unconfirmed`
item without `verifying` is settled: no check could answer.

The `error` and `message` of an item, and the `message` of each node, are
cleaned where they enter the run, so the REST routes, the Updates page, the
inbox, the history and the assistant all get the same text: one line of at
most 512 characters, with no control characters or markup, each absolute or
home-directory path cut to its file name, and the user and password of a URL,
bearer credentials and values such as `token=` or `password=` removed.

A failure has an error key: `download`, `verify`, `swap`, `start`,
`not_installed`, `no_update`, `held`, `edited_keys`, `stale` or `unknown`. A
failed add-on keeps the failure code of the add-on owner, for example
`upgrade_failed` or `recovery_required`.

A backend item takes the error key and the rollback from its operation (see
[Starting an upgrade]({{% relref "/models/backends#checking-for-backend-updates" %}})):
a new build that did not start and was replaced by the previous one ends the
item `rolled_back` with the key `start`. The error of such an item is the
cause alone (for example "the backend process exited before it answered a
health check (exit code 3): ..."): the key already says that the new build did
not start, and the Updates page and the inbox put that sentence before the
cause. An operation whose worker reply never
came ends the item `unconfirmed`, and a fresh check decides as above. In a
cluster, `nodes` lists the result of each node: `id`, `name`, `phase`
(`success`, `error`, `rolled_back`, `unconfirmed`, or a phase of a node that
still runs), `progress`, `message` and `error_key`. The frontend does not trust
the text a worker sends: it keeps at most the last 512 bytes of the error, the
cause and the restart error of each reply and cleans them as below, and a code
it does not know becomes `unknown`.

## Cancel, retry and dismiss

- Cancelling a queued item removes it from the run. A running item can be
  cancelled only while its operation says it can stop. For example, a model
  upgrade cannot stop while it switches the files. The `cancellable` field of
  an item is `true` only for a running item whose operation can stop now; a
  queued item has `cancellable: false` and can always be cancelled.
- Cancelling the run cancels the queued items and stops the running ones when
  they can stop. The run then ends `cancelled`.
- Retry starts a new run with the retryable items of a finished run that
  failed, rolled back or stayed unconfirmed, if they still have an update. The
  old run does not change. A run that is over changes only when it is
  dismissed. The new run is a `manual` run of the administrator who retries:
  its checks and its add-on items run as that administrator, never as the one
  who started the old run. The retry of an automatic run is a `manual` run too,
  so its add-on items go through the path of an administrator, with the
  digests pinned, and not through the automatic upgrade.
- Dismiss hides a finished run from the default list.

An item is `retryable` when a new try can fix its failure without a person
doing something first. The error key decides:

| Retryable | Error keys |
|-----------|------------|
| Yes | `download`, `verify`, `swap`, `start`, `stale`, `unknown`, `unavailable`, `interrupted` (also an item that a restart stopped), and the add-on codes `upgrade_failed`, `stale_installed`, `candidate_changed` and `check_failed` |
| No | Every other key, for example `incompatible`, `integrity_error`, `recovery_required`, `forbidden`, `not_human_surface`, `held`, `edited_keys`, `approval_required`, `approval_stale`, `upgrade_conflict` and `operation_conflict` |

An `unconfirmed` item is retryable: the retry checks it again. The Updates
page shows **Retry** only on a retryable item.

## Restarts

The run and its items are stored in the database. When the server restarts,
it takes over the runs that were running. The operations of the old process
stopped with it, so each item that was running becomes `unconfirmed` and a
fresh check decides: `updated` if the item is no longer listed, `failed` with
`interrupted` if it is ("the update was interrupted by a restart"). The item
is retryable. Queued items continue.

A crash in the middle of a backend install or upgrade can leave files on
disk. At start, before any operation can run, a server that is not part of a
cluster cleans them: it removes the staged new build next to the backend
(`<backend>.upgrade-tmp`, `<backend>.install-tmp`) and the partial downloads in
the download staging directory (`LOCALAI_DOWNLOAD_STAGING_PATH`). It never
removes a previous build (`<backend>.backup`, `<backend>.install-backup`): when
the backend directory is missing, the previous build is put back; when both
are there, the server logs it and leaves both, because the backend in place
may be a new build whose start check never ran. A build moved aside because it
did not start (`<backend>.failed`) stays too. The next install or upgrade of
the backend does not delete a previous build it finds either: it moves it to
`<backend>.backup.kept-<time>` (or `.install-backup.kept-<time>`) and logs it,
and refuses to start when it cannot move it. Remove a kept build once the
backend works.

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
  run as after a restart. A frontend that loses its lock but keeps running
  (a lost database session) leaves its operations running: the frontend that
  takes the run over follows an item whose operation still has a status, and
  checks only the others. When that status shows no change for two minutes,
  the frontend that ran it is taken as gone and the item is checked too. From the takeover on, the run rows refuse the writes
  of the frontend that lost the run.
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

### Retention

The server removes finished runs (`finished` or `cancelled`) and their items
that ended more than 30 days ago, and keeps at most the newest 200 finished
runs. A running run is never removed and does not count. Dismissing a run
only hides it; the retention removes it later.

| Flag | Default | Environment variable |
|------|---------|----------------------|
| `--upgrade-run-retention-days` | `30` (`-1` keeps runs of any age) | `LOCALAI_UPGRADE_RUN_RETENTION_DAYS` |
| `--upgrade-run-keep` | `200` (`-1` keeps any number) | `LOCALAI_UPGRADE_RUN_KEEP` |

The removal runs once an hour from the loop that takes over the runs nobody
runs. In a cluster, it takes the advisory lock that admits a new run, so one
frontend at a time removes runs; the others skip that round.

## Notifications of a run

Every run is one item in the inbox of each administrator. The frontend that
runs the run writes it, so a cluster writes one item for each run.

- **While the run runs: the live item** (code `updates.run`, key
  `updates.run.<id>`). It is created when the run starts and edited in place
  on every change, so it never grows into a list. It never notifies (no
  browser push, no connectors), not even when it is created. It writes at most
  once every 1.5 seconds for each run; the last change of a burst is always
  written. It links to the run (`/app/updates?run=<id>`).
- **When the run ended: the end item** (code `updates.run.end`, key
  `updates.run.end.<id>`). It is written once, when the run ends, and notifies
  once (browser push and connectors). Then the live item is removed, so the
  end item is the only item of the run. It says how many items were updated
  and how many need attention. When any needs attention, it is a failure and
  names the first one with its error. The error is cleaned as the item error
  is, and cut to 160 characters.

An item needs attention when it `failed`, `rolled_back`, stayed
`unconfirmed`, or was `skipped` with an error key (for example `held`,
`edited_keys` or `not_human_surface`). A `cancelled` item does not: a person
asked for it. The result at the top of the Updates page counts the same items.

The operations of the items do not write their own "installed", "upgraded" or
"failed" items: the item of the run replaces them. This holds for every kind,
a model upgrade included. An operation started
outside a run (an install, a removal) still writes its own item when it ends.

The live item has these params: `run`, `progress` (0 to 100 for the whole run),
`done`, `total`, `source` (`manual` or `auto`), `updated`, `running` and
`queued`.

```json
{"code": "updates.run", "key": "updates.run.2f0c6a52", "link": "/app/updates?run=2f0c6a52",
 "params": {"run": "2f0c6a52", "progress": 40, "done": 1, "total": 3, "source": "manual",
            "updated": 1, "running": 1, "queued": 1}}
```

The end item has `run`, `total`, `updated`, `failed` and `source`. A run that
was cancelled also has `state: "cancelled"` and `cancelled`, the number of
items that did not run, and its title says "Update run cancelled: 1 updated".
When `failed` is more than zero it also has `first_name`, `first_from`,
`first_error` and `first_key`. `first_name` is the title of an add-on,
`first_from` is the version the item is still on (a digest only when there is
no version), and `first_key` is the error key of that item.

```json
{"code": "updates.run.end", "key": "updates.run.end.2f0c6a52", "link": "/app/updates?run=2f0c6a52",
 "params": {"run": "2f0c6a52", "total": 3, "updated": 2, "failed": 1, "source": "auto",
            "first_name": "kokoro", "first_from": "v1.2.0", "first_error": "the download failed"}}
```

## Automatic updates are runs

The automatic update of backends (setting **Update backends automatically**)
and of add-ons (**Update add-ons automatically**) starts a run with the source
`auto`. The run shows in the Updates page and in `/api/operations` like a run
that a person started, and has the notifications above. Both settings are off
by default.

- **Backends.** A scheduled check lists the gallery and library backends that
  have an update, and starts one run for all of them. The check no longer
  installs inside its own pass.
- **Add-ons.** The add-on pass starts one run for the add-ons it would upgrade
  now. Each add-on item goes through the add-on owner's own automatic upgrade,
  with the same rules as before: the owner's system actor, the digests pinned,
  no approval given for anybody, and a release that failed is not tried again
  until the library lists another one. A release that needs host access nobody
  approved stays on the old version. Its item ends `failed` with a message that
  starts with "held", and the end item of the run counts it as needing
  attention. Approve the upgrade on the add-on page.
- **Only the add-on owner adds add-ons.** The add-on pass runs on the process
  of the add-on owner, and an automatic run lists and upgrades add-ons there,
  on the owner itself, as the system actor of the automatic upgrade. That
  actor never goes through the add-on gateway, which refuses it. Runs that a
  person starts keep their path as that person. A process that is not the
  owner can start the run of the backends, and never has add-on items.
- **A release that did not start is held back.** An automatic run leaves out
  an item whose target (the version or digest in `expected_to`) already ended
  `rolled_back`, or `failed` with the key `start`, `verify` or `swap`, in the
  newest attempt of that release in one of the last 20 runs, dismissed runs
  included. A new try of the same release ends the same way, so without the
  hold every pass ran its start check again and added an item to the inbox.
  Other failures (`download`, `unavailable`, `unknown`, `interrupted`, ...) are
  tried again. The hold ends when the gallery lists another release. A run
  that a person starts is never held back: start the item from its row to try
  the release again. When every item of a pass is held back, no run starts and
  nothing is written. The Updates page shows a held-back backend as
  "Held back: this release did not start", and `GET /api/backends/updates`
  marks it with `heldBack: true`.
- **One run at a time.** When a run is already running, the pass does not
  start another and does not write anything. It tries again every minute,
  and at the latest at the next scheduled pass, which lists the same items
  again. No update is lost.
- **Results.** A pass that started a run does not write its own result item
  ("Backends: 2 updated automatically"): the end item of the run replaces it.
  The digest of the updates that need a person and the item of a failed check
  stay as they are.
- **Without the run service.** A server that keeps no runs (see
  [Where the runs are kept](#where-the-runs-are-kept)) installs inside the pass
  and writes the result items, as before.

## Add-on items

An add-on item goes through the same path as
`POST /api/addons/{id}/upgrade`. The add-on owner records the upgrade for the
administrator who started the run, with the installed digest and the candidate
digest pinned, and checks it as it checks the explicit upgrade. The operation
queue then runs it for that administrator, and the owner checks the pins again
when it starts. A run that a person started never upgrades an add-on as a system identity.
The automatic run is the exception: it uses the owner's own automatic upgrade
(see [Automatic updates are runs](#automatic-updates-are-runs)).

The explicit upgrade is accepted only from the administrator's own browser
session. A run keeps that rule: its add-on items run only when the request
that started the run came from that session, with the header
`X-LocalAI-Confirm: upgrade-run`. Otherwise each add-on item ends `skipped`:

| Error key | Why |
|-----------|-----|
| `not_human_surface` | The run was started with an API key, by the assistant, or without the confirmation header. |
| `forbidden` | The account that started the run is not an add-on administrator. |

These two keys end the item `skipped`. `unavailable` (the add-on owner did not
answer) ends it `failed`, and a retry takes it.

A refusal of the owner ends the item `failed` with the type of the owner, for
example `stale_installed`, `candidate_changed`, `incompatible` or
`upgrade_conflict`. An upgrade that needs host access nobody approved fails
with the failure code of the owner (`approval_required`); approve it on the
add-on page. A retry of a run with add-on items needs the same browser
session and header.

The list of add-ons with an update comes from the add-on owner, read through
the add-on gateway as the account that asked: the owner answers on its own
process, and every other frontend forwards the read to it, as the add-on
routes do. A preview, a start and the checks of a run see the same add-ons on
every frontend. A run whose actor is not an add-on administrator cannot list
add-ons, so a start of an add-on item is refused with `source_unchecked`. When
the owner cannot be reached, a start of an add-on item is refused with
`source_unchecked`, and an add-on item that was about to start ends `failed`
with the error key `unavailable` (retryable).

## Access and trust

- **Any administrator can read and cancel any run.** The run tools are
  shared admin tooling: every administrator sees every run, its items and its
  errors, and can cancel a run or an item that another administrator or the
  automatic update started. A retry is a new run of the administrator who
  retries.
- **The assistant asks before it starts or cancels a run.** The tools
  `upgrade_run_start` and `upgrade_run_cancel` rely on the rule of the
  assistant's safety prompt that it confirms with you before it changes
  anything. That rule is in the prompt, not in code, as for every mutating
  admin tool. Add-on items are refused in code: `upgrade_run_start` does not
  take them.
- **The start check runs the new build.** After the swap, the server starts
  the downloaded backend build once, as the user LocalAI runs as, to check
  that it starts (see
  [When the new build does not start]({{% relref "/models/backends#checking-for-backend-updates" %}})).
  Without strict integrity (`--require-backend-integrity` off), a build from a
  gallery entry with no SHA-256 and no signature policy is accepted, so such a
  build runs at upgrade time, and not only at the next model load.

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
| `held_back` | `409` | Only an automatic run: every item is a release that did not start before (see [Automatic updates are runs](#automatic-updates-are-runs)). |
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
  -d '{"items": [{"kind": "gallery", "name": "kokoro"}, {"kind": "model", "name": "qwen3-8b"}, {"kind": "addon", "name": "notes"}]}'
```

```json
{
  "items": [
    {"key": "gallery:kokoro", "kind": "gallery", "name": "kokoro", "from": "v1.2.0", "to": "v1.3.0",
     "expected_to": "v1.3.0", "stage": 1, "order": 0, "startable": true,
     "risks": ["stops_models"], "selected": true, "default_selected": true,
     "risk": [{"code": "stops_models", "params": {"count": "2", "nodes": "2"}}]},
    {"key": "model:qwen3-8b", "kind": "model", "name": "qwen3-8b", "from": "sha256:aa", "to": "sha256:bb",
     "expected_to": "sha256:bb", "stage": 2, "order": 1, "startable": true,
     "risks": [], "selected": false, "default_selected": false, "risk": []},
    {"key": "addon:notes", "kind": "addon", "name": "notes", "title": "Notes", "from": "0.8.1", "to": "0.8.2",
     "expected_to": "sha256:5d1f...", "stage": 3, "order": 2, "startable": true,
     "risks": ["migrates_data"], "selected": true, "default_selected": true,
     "risk": [{"code": "migrates_data", "params": {}}]}
  ],
  "order": ["gallery:kokoro", "model:qwen3-8b", "addon:notes"],
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
the id of the account that started the run (for a retry, the account that
retried). An add-on item also has `title`,
and its `expected_to` is the digest it is pinned to. Empty fields are left out.

## The single upgrade routes

These routes keep their answers for existing clients. Each starts a run of one
item and adds the run to its answer:

| Route | Answer | Added |
|-------|--------|-------|
| `POST /api/backends/upgrade/{name}` | `200`, `jobID`, `uuid`, `statusUrl`, `message` | `run` |
| `POST /backends/upgrade/{name}` | `200`, `id`, `status_url` | `run` |
| `POST /api/models/upgrade/{name}` | `200`, `id`, `jobID`, `status_url`, `message` | `run` |
| `POST /api/addons/{id}/upgrade` | `200`, the operation status with `jobId` | `run` in the payload |

The backend routes first refuse a request that cannot do anything, before any
run starts: `404` `not_installed`, or `409` `no_update` or `stale` (see
[Starting an upgrade]({{% relref "/models/backends#checking-for-backend-updates" %}})).
While a run runs, every route answers `409` with code `run_active` before it
checks anything else (the add-on route in its error type), so no upgrade runs
beside a run. The backend routes take an optional `expected_to`, in the query
or the JSON body, and answer `409` `stale` when it moved. When the item cannot
start, they answer `409` with the error key of the item as `code`. When no run
runs and the check of the run does not list the item (it was not checked yet,
or an older listing), or the server keeps no runs, they queue the upgrade
directly, as before.

Each route waits up to 30 seconds for the run to get the operation accepted.
When the wait ends first, the route answers `202` with code `pending`,
`run_id` and `run`, and no job: the run goes on and starts the item later.
Follow it with `GET /api/updates/runs/{run_id}`. The add-on route answers
`202` with the operation status, `run` and `runId` in the payload, and no
`jobId`; the run then runs the operation the route recorded. The run stores
that operation with the item, so a frontend that takes the run over runs the
same record. When the item ends without starting it (cancelled, skipped or
refused), the run drops the record on the owner and the add-on is free for a
new upgrade.

A model upgrade with `config_only` or `choices`, and a model retry
(`POST /api/models/{name}/upgrade/retry`), are always direct: a run makes no
such choice. While a run runs, they are refused with `409` `run_active` too.

The per-node route `POST /api/nodes/{id}/backends/upgrade` is the one
exception: it upgrades a backend on one node, directly, and does not start a
run or check for one. Use it to repair one node; it does not show in a run or
in its notifications.

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
of one item too. When the run did not start the operation within the wait,
they answer with an error that names the run to follow, never a job.
`upgrade_backend` refuses a backend that is not installed or
has no update before it starts anything, as the REST routes do. While a run
runs, every upgrade from the assistant (also `upgrade_model` with the actions
`config_only` and `retry`, and `install_library_backend`) is refused with
`run_active`. `POST /backends/library/install` is refused the same way.
