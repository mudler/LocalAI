+++
disableToc = false
title = "Model Updates"
description = "Find out when the gallery changed a model you installed, and upgrade it safely"
weight = 85
+++

A gallery entry can change after you install it. The weights can move to a new
build, the configuration can change, or a checksum bot can rewrite a hash.
LocalAI checks for this and tells you. It never changes a model by itself: **a
model upgrades only when you ask for it.**

This page covers gallery models. Backends have their own updates; see
[Backends]({{% relref "/models/backends#the-updates-page" %}}).

## What counts as an update

When LocalAI installs a model from a gallery, it records where the model came
from and a digest of what the entry installed. The digest covers:

- the files of the entry (name, URI and sha256 of each),
- the configuration the entry writes (`config_file`, `overrides` and the base
  configuration it points to, with its prompt templates),
- the variant that was installed.

An update is a new digest. A change of the description, the icon, the license,
the tags, the size or the URLs of the entry does not change it.

Only models that were installed from a gallery entry are tracked. A model that
you imported from a URL or a configuration file has no entry to compare with.

## The states

**Admin → Updates** lists each tracked model with one of these states. The
Models page shows the same state on each model.

| State | Meaning |
|-------|---------|
| `up-to-date` | The entry is the same as at install time. |
| `update-available` | The entry changed. The update does not overwrite anything you edited. |
| `edited` | The entry changed, and it would overwrite keys that you edited in the model configuration. You choose a value for each of them before the upgrade starts. |
| `unknown` | The check cannot compare. The reason is one of: the model was installed before this feature (see below), the check has not run yet, the check could not read the base configuration of the entry, or the model files cannot be read. |
| `gone` | No configured gallery lists the entry any more. The installed version keeps working. |
| `held` | You hold this version. LocalAI offers no update for it. |
| `failed` | The last upgrade did not complete. The old version serves. The row has the error and **Retry**. |

A model that you installed with an earlier release has no record of its origin.
It shows as `unknown` (reason `legacy-sidecar`) until you **adopt** it, or
install it again from the gallery.

### Adopting a model installed by an earlier release

Adopt records the gallery entry a model came from, so that LocalAI checks its
updates like those of a model installed now. It writes only the record of the
model:

- the gallery and the entry,
- the files the model has, as its old record lists them,
- what the gallery would write as the configuration of the model now.

Adopt does not change the configuration on disk. A key that differs from what
the gallery would write reads as an edit at the next check, so your changes
stay protected by the [edited keys](#edited-keys) rules. A value that an older
version of the entry wrote and that the entry changed since also reads as an
edit; choose **Take the gallery's** for it at the upgrade.

LocalAI finds the entry by the name of the model (and the entry name in its
old record) in the configured galleries. It adopts only when exactly one
gallery lists that entry. Otherwise the request is refused with the list of
candidates (`gallery@entry`), and you send it again with the gallery, and the
entry when its name differs from the model's. When the files of the model are
not the files the entry lists now, the next check shows `update-available`.
Adopt does not run a check: press **Check now** to see the new state.

## Checking

LocalAI checks 30 seconds after the server starts and then every six hours. The
interval is not a setting. A check reads the gallery indexes again (it does not
use the copy that LocalAI keeps in memory for up to an hour) and compares each
tracked model with its entry. It installs nothing.

**Check now** on the Updates page runs a check at once and waits for the
answer (at most 60 seconds). A check that ended less than 15 seconds before
answers from that result and is marked `throttled`. If the check fails, the
gallery line on the Updates page shows the error and the list keeps the result
of the last good check.

## Where you see it

- The **Updates page** (**Admin → Updates**) has a row for each model that
  needs you or is changing. Held models, `unknown` and `gone` models, and
  models that still keep an old version, are in the group **Other models**.
  Models never take part in **Update N ready**: you upgrade them one at a time, each after its own confirmation. Upgrades you start are queued on the server and run in the background, so you can close the page. Upgrades of different models run in parallel, up to the limit of concurrent model operations; the rest wait as queued and can be cancelled. **Update N ready** (backends, libraries and add-ons) submits every row at once the same way, with the limit of concurrent backend operations, and its **Cancel all** button cancels the rows that have not finished. Both limits default to 2: see [Concurrent Model Operations]({{% relref "/administration/runtime-settings" %}}).
- The **notification center** gets three kinds of item for administrators. They
  work with the chats on or off, and they are not sent to a Telegram or Slack
  chat.
  - A digest, "N model updates available". It lists the models in the states
    `update-available`, `edited` and `failed`. It is kept up to date in place
    and removed when none is left.
  - The end of an upgrade, "Model X upgraded" or "Model X: the upgrade failed"
    with the reason. It is written once for each upgrade operation, also when
    the upgrade ends before it changes anything: the model is up to date, the
    entry is gone, a check of the preflight refuses it, edited keys need a
    choice, or the model is held. A cancelled or paused upgrade is told as
    "cancelled", not as a failure: the model keeps the state it had before.
  - "Model X: the old version was removed", after **Remove old version**.
- The [LocalAI Assistant]({{% relref "/features/chats-and-agents/localai-assistant" %}})
  has the tools `model_updates` and `upgrade_model`.

See [the inbox]({{% relref "/features/chats-and-agents/chats-agents#inbox" %}})
for how notification items behave.

## Upgrading a model

**Upgrade** on a row opens a confirmation. It lists the files that change, says
whether the model is loaded now, and, for an `edited` model, asks for a choice
for each key. Nothing starts until you confirm.

### Edited keys

LocalAI compares the configuration on disk with what the install wrote. A key
that you changed is an edit. An edit that the update does not touch is kept
without asking. For an edit that the update also changes, you choose:

- **Keep mine**: the key keeps your value.
- **Take the gallery's**: the key gets the new value.

The upgrade does not start while one of these keys has no choice.

If the record of the model has no copy of the configuration that the install
wrote, LocalAI cannot tell an edit from a value of the old version. The upgrade
then writes the gallery's new configuration and keeps only the operator state
(`pinned` and `disabled`) from the configuration on disk.

### Config-only upgrades

When an update changes no model file (only the configuration or the prompt
templates), nothing needs to download. The dialog then offers **Apply only the
config and the prompt templates**. A config-only upgrade is refused when the
new configuration needs a file that is not on disk.

### What happens during an upgrade

An upgrade runs as an operation, like an install. It reads the gallery indexes
again when it starts, so it installs what the entry gives now, also on a
frontend of a cluster that did not run the last check. Its phases are:

1. **Preflight.** LocalAI checks that the backend the new version needs is
   installed, that no file to replace is used by another model, that the gallery
   gives a sha256 for each new weights file, and that the disk has room for the
   new files (when the server tells the file size). A failed check ends the
   upgrade here, with nothing changed.
2. **Staging.** The new files download next to the old ones under a `.next`
   name, and LocalAI checks each sha256. The model keeps running on the old
   version.
3. **Unloading.** LocalAI refuses every new load of the model, and if the
   model is loaded, it stops. From here until the new version can load, a
   request to the model is answered `503` with a `Retry-After` header. A
   request cannot start the old version again while the files change.
4. **Committing.** LocalAI keeps each old file as `<file>.prev-<digest8>`
   (`digest8` is the first eight characters of the old digest), moves the new
   files into place and writes the new configuration.
5. **Loading.** LocalAI reads the new configuration, stops any instance of the
   model that still runs (for example a load that was already starting when
   the upgrade stopped the model), and allows loads again. Then the new version
   loads, as a check. A change of the weights alone does not change the
   configuration, so this stop does not depend on it. A model that was not
   loaded before the upgrade is stopped again after the check.
6. **Confirmed.** The upgrade is done.

You can cancel or pause an upgrade only in the first two phases. After the
model stops, the upgrade runs to its end (confirmed or rolled back), and
LocalAI refuses a cancel or a pause: `POST /api/operations/{id}/cancel` and
`/pause` answer `400` with the reason. A cancel removes the partial downloads.
A pause keeps them, so the next upgrade to the same version resumes them. If
the gallery changed in between, the resumed file fails its checksum and the
upgrade fails; run it again.

If the server stops during an upgrade, it ends the upgrade at the next start
(in a cluster, another frontend ends it within minutes; see
[Distributed mode]({{% relref "/administration/distributed-mode#model-updates-in-a-cluster" %}})).
Before the unloading phase it removes the staged files and the upgrade ends
`failed`. From the unloading phase on, it puts the old version back.

### An edit during an upgrade

The upgrade merges the new configuration with your configuration when it
starts. If you save a change to the model configuration while the upgrade
runs, LocalAI does not overwrite it:

- An edit before the switch (during the download, or between the stop and the
  switch) ends the upgrade with the error "the model config was edited while
  the upgrade ran". Nothing of the upgrade is applied and your edit stays. If
  the model was stopped, the old version loads again. Run the upgrade again to
  merge your edit with the update.
- An edit after the switch, while the new version loads, is kept if the load
  fails and the upgrade rolls back: the old files come back, but the
  configuration stays as you saved it, and the error of the upgrade says so.

### A failed load and the rollback

If the new version does not load, LocalAI puts the old version back: the old
configuration (from a copy that it keeps in its record of the model while the
check runs), the old files, and the old record. It then loads the old version
again if the model was loaded. The state of the model is `failed`. The row and
the notification show the error. Fix the cause, or wait for a new gallery
entry, and press **Retry**. A retry uses the same options and choices and aims
at what the gallery entry installs now.

If the rollback itself fails, the error names both failures. Check the model
files before you retry.

### The old version stays

After a confirmed upgrade, the old files stay on disk as `.prev-<digest8>`
files. The row says "Old version kept" and has **Remove old version**. That
action deletes those files after a confirmation and cannot be undone. It does
not change the model configuration. A file that another model uses is not
deleted.

The old configuration is not kept after the load check passes. The old
*files* are.

## Hold this version

**Hold this version** (the row menu, or the Updates card on the model's own
page) stops LocalAI from offering an update for that model. The state becomes
`held`. **Release hold** undoes it. A hold changes only the record of the model:
the configuration and the loaded model stay as they are, and a reinstall keeps
the hold. You cannot change a hold while an upgrade of the model runs.

The server enforces the hold. An upgrade or a retry of a held model is refused,
from the REST API (`409`, "release the hold before you upgrade it") and from
the assistant, as well as in the web UI. An upgrade that was queued before the
hold was set is refused when it starts. Release the hold, then upgrade. In a
cluster, a hold and an upgrade of the same model take the same lock, so a hold
set on one frontend is never lost to an upgrade on another.

A hold is not a pin. A **pin** keeps a model loaded and out of the eviction
list (see [VRAM and Memory Management]({{% relref "/models/vram-management" %}})).
A hold only says "do not offer me an update". Both can be set on the same model.

## Known limits

- There is no way back to an earlier version after the upgrade is confirmed.
  Rollback happens only when the new version fails its load check. The old
  files stay until you remove them, but LocalAI does not restore them for you.
- A Hugging Face artifact snapshot (a model that the gallery points at a
  Hugging Face repository) is not part of the staged files. An upgrade does not
  keep the old snapshot, does not roll it back, and **Remove old version** does
  not delete it.
- An upgrade is refused when the new version needs a backend that is not
  installed. Install the backend first.
- The new weights need a sha256 in the gallery entry. Without one, the upgrade
  does not download them.
- In a cluster, the strategy `canary` is stored but not active yet. See
  [Distributed mode]({{% relref "/administration/distributed-mode#model-updates-in-a-cluster" %}}).

## REST API

All routes need an administrator. The same routes serve the web UI. Their
definitions are in the API reference (Swagger).

List the models and their states:

```bash
curl http://localhost:8080/api/models/updates
```

```json
{
  "checkedAt": "2026-11-19T10:00:00Z",
  "sources": {"gallery": {"state": "ok", "checkedAt": "2026-11-19T10:00:00Z"}},
  "items": [
    {
      "id": "model:qwen3-4b",
      "kind": "model",
      "name": "qwen3-4b",
      "state": "edited",
      "needsPerson": true,
      "gallery": "localai",
      "entryName": "qwen3-4b",
      "changedFiles": [{"path": "qwen3-4b-q4.gguf", "role": "weights", "change": "changed", "checksum": true}],
      "editedKeys": [{"key": "context_size", "installed": 4096, "mine": 8192, "gallery": 32768, "conflict": true}]
    }
  ]
}
```

Check now. The call waits for the check and installs nothing:

```bash
curl -X POST http://localhost:8080/api/models/updates/check
```

Upgrade a model. The body is optional. `config_only` applies only the
configuration and the templates. `choices` gives `mine` or `gallery` for each
conflicting key, by dotted key name:

```bash
curl -X POST http://localhost:8080/api/models/upgrade/qwen3-4b \
  -H "Content-Type: application/json" \
  -d '{"config_only": false, "choices": {"context_size": "mine"}}'
```

```json
{"id": "<job>", "jobID": "<job>", "status_url": "http://localhost:8080/models/jobs/<job>", "message": "Upgrade started"}
```

Follow the job on `status_url` or on `/api/operations`. The route answers:

| Status | Meaning |
|--------|---------|
| `404` | The model is not installed. |
| `409` | An upgrade of this model runs, the model is held, or edited keys need a choice. The last case lists the keys in `keys`. |
| `422` | The model was not installed from a gallery with a recorded origin. |
| `400` | A choice is not `mine` or `gallery`. |
| `500` | An unexpected error. The answer does not repeat its text, which can name paths of the server; the server log has it. |

Errors of an upgrade, in the API, in the record of the model and in the
notifications (a browser push included), name files relative to the models
directory, never by their full path.

A refusal of the preflight (no backend, no checksum, not enough disk) does not
come from this call. It ends the operation with an error, and nothing changes.

Retry a failed upgrade. The call has no body. It answers `409` when the last
upgrade did not fail, or when the model is held:

```bash
curl -X POST http://localhost:8080/api/models/qwen3-4b/upgrade/retry
```

Remove the old version after a confirmed upgrade:

```bash
curl -X POST http://localhost:8080/api/models/qwen3-4b/remove-old
# {"name": "qwen3-4b", "removed": ["qwen3-4b-q4.gguf.prev-1a2b3c4d"]}
```

When only some of the old files can be removed (a file that another model uses
now stays), the call answers `200` and adds `kept`, the files still listed for
a later removal, and `errors`, the reason for each. When none can be removed it
answers `409`.

Hold or release a model:

```bash
curl -X PUT http://localhost:8080/api/models/qwen3-4b/hold \
  -H "Content-Type: application/json" -d '{"hold": true}'
```

Adopt a model that an earlier release installed. The body is optional; without
it the entry is found by the model's name:

```bash
curl -X POST http://localhost:8080/api/models/qwen3-4b/adopt \
  -H "Content-Type: application/json" -d '{"gallery": "localai"}'
# {"name": "qwen3-4b", "gallery": "localai", "entry": "qwen3-4b", "filesMatch": true}
```

It answers `409` with `candidates` when more than one gallery lists the entry,
when the model already records its origin, or while an upgrade of it runs, and
`422` when the model has no record of a gallery install or no gallery lists the
entry (with the `candidates` of other galleries, when there are some).

### The old rewrite route is gone

`POST /api/models/config/{id}` used to rewrite a model configuration from its
gallery entry at once, with no check and no backup. It now always answers
`410 Gone`, with a `Link` header that points to `POST /api/models/upgrade/{name}`.
To get the same result, upgrade the model with `{"config_only": true}`. The
`410` answer stays for one release.

## From the assistant

The [LocalAI Assistant]({{% relref "/features/chats-and-agents/localai-assistant" %}})
can do the same from a chat:

- `model_updates` lists the models and their states. With `action` `check` it
  reads the galleries now.
- `upgrade_model` takes the `name` of a model and an `action`: `upgrade`,
  `config_only`, `retry`, `remove_old`, `hold`, `unhold` or `adopt`. `choices`
  applies to `upgrade` and `config_only`; `gallery` and `entry` apply to
  `adopt`. The assistant asks you to confirm before it runs
  it.

## Model updates in a cluster

A cluster needs more steps for the same upgrade. See
[Model updates in a cluster]({{% relref "/administration/distributed-mode#model-updates-in-a-cluster" %}}).
