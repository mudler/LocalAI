+++
disableToc = false
title = "Activity"
description = "Watch, cancel and retry model and backend installs from the WebUI"
weight = 60
aliases = ["/features/activity/"]
+++

Model installs, backend installs, updates, removals and cluster staging all run
in the background. Two surfaces in the WebUI report them: the **Activity** tab
of the notification panel, and the **Activity** page, which holds the full
picture.

Both are admin-only.

To see models currently loaded for inference, open **Home** (`/app`) and scroll
to **Loaded models**. That list includes each model’s backend and a stop control.
A loaded model can be idle; Activity reports management jobs rather than whether
a model is resident or generating output.

## The Activity tab

The **Notifications** entry in the sidebar (the bell in the header on a phone)
opens the notification panel. For an administrator the panel has two tabs,
**Notifications** and **Activity**. Everyone else sees the panel without tabs,
as before. When authentication is off, everyone is an administrator.

The panel opens on **Activity** while something is running, queued or failed,
and on **Notifications** otherwise. A tab you pick stays picked while the panel
is open. The tab has three sections, each shown only when it has something in
it:

- **Needs attention**: failed operations, kept until you dismiss them. Each row
  shows the reason, with **Retry** and **Dismiss**.
- **In progress**: one row for each running or queued operation. A row has a
  progress ring, the title (`Installing qwen3-4b`, `Updating llama-cpp`,
  `Removing whisper`), one detail line, a thin bar with the percentage, and
  **Cancel**. A queued operation says `Waiting`. The detail line joins what the
  operation reports, for example `Node 3 of 5 · Downloading · 1.2 GB / 1.9 GB ·
  48 MB/s`: the node a fan-out install has reached, the phase (`Resolving
  files`, `Downloading`, `Verifying`, `Finalizing`, `Saving configuration`),
  the bytes and the speed. A part the operation does not report is left out.
- **Recent**: operations that finished successfully in the last day, newest
  first, at most five, each with how long ago it finished (`Installed qwen3-4b`,
  `Updated llama-cpp`, `Removed whisper`). A fast install shows here, so it does
  not vanish before you see it. A cancelled operation, and a failure you
  dismissed, are not listed. The Activity page keeps them in its record. Nothing
  is written to the notification list for a finished operation.

**See all activity** at the bottom opens the Activity page. The tab scrolls
inside the panel when it is long.

The sidebar entry shows the operations too. While operations run, the icon sits
in a small ring that fills with the average progress of the running operations,
and it turns while only queued operations wait. If an operation failed, the ring
is red and has a dot. The number badge counts unread notifications only;
operations never change it. With nothing running or failed, the entry looks as
it always did.

**Retry** and **Cancel** behave as on the Activity page (see below). The **Cancel**
wait of 8 seconds applies here too, with the same **Undo** toast. Closing the
panel sends a cancel that is still waiting, as leaving the page does; changing
tab does not.

Where the chats feature is off there is no notification inbox. An administrator
still gets the entry, named **Activity**, while an operation runs or failed,
shortly after one finished, and for the rest of the visit once one has been
seen. It opens the Activity tab alone, with no notification list, filters or
browser-notification switch, and it asks the chats for nothing. Anyone else,
and an administrator with nothing to show, sees no entry; use the Activity page.

## The Activity page

**Operate → Activity** (`/app/activity`). The page has up to three sections,
each shown only when it has something in it.

### In progress

One row per running or queued operation, each naming what is being done and to
what. A percentage and progress bar appear only once the operation is running
and has reported progress, so a queued operation and a removal show none.
Artifact-backed gallery models also report the phase, downloaded and total
bytes, and an estimate of the time left once enough of the transfer has been
observed to work one out. Every backend install, and every gallery model that
lists its files directly, reports no phase and no byte counters; those cards
show the installer's own status line instead, which names the file being
fetched and its size.

An install that involves workers shows a per-node list, with one row per
worker:

- a status pill: **Queued**, **Downloading**, **Worker busy**, **Done** or
  **Failed**;
- the file being transferred, with current/total bytes and a percentage;
- any error the worker returned.

An install that fans out to more than one worker also carries an **N nodes**
tag and a toggle over the list. Lists of up to four nodes start expanded, longer
ones start collapsed behind **Show N nodes**, and the toggle reads **Hide
per-node detail** while the list is open.

**Worker busy** means the worker took longer than `--backend-install-timeout`
to acknowledge but is most likely still working. It clears on its own when the
worker finishes.

### Needs attention

Model and backend operations that failed and have not been acknowledged yet,
each row carrying the error returned by the installer, with **Retry** and
**Dismiss**. Cluster staging never
appears here: a staging job reports no error to the page, so a staging failure
has to be read from the logs.

### Record

What finished, newest first, one row each: the name, what happened
(`installed in 1m 12s`, `removed`, `cancelled`, or `failed:` with the error),
the time of day it finished, and a link into Models or Backends. A cancelled
install also has **Start again**, which installs the same target again: a
download that was paused continues where it stopped, and one that was cancelled
starts over. The record does not say which of the two it was, so the button
says what it does in both cases. A cancelled removal has no such button. Model and
backend installs and removals are recorded; cluster staging is not, so a
staging run leaves nothing behind here once it finishes.

### Filters

Four chips above the sections filter the whole page: **All**, **Models**,
**Backends** and **Cluster**. In the live sections, Cluster covers staged model
files and any install that involves workers, whether it targets one node or
fans out across several. In the Record it covers only the node-targeted case: a
finished fan-out install is filed under Models or Backends, not under Cluster.

## Cancelling, retrying and dismissing

These are on the operation rows. **Pause**, **Cancel**, **Retry** and
**Dismiss** are labelled buttons. The Activity tab has **Cancel**, **Retry**
and **Dismiss**; **Pause** is on the page only. The Backends page also shows the
progress of a backend install in the backend's own row, with its **Cancel**.

- **Pause** stops a running download and keeps the bytes already fetched, so
  installing the same model or backend again continues from there.
- **Cancel** is offered while an operation is queued, whatever it is, and while
  an install is running. It is not offered once a removal has started: a
  removal in progress cannot be interrupted, so the window to call one off is
  the queue, before a worker picks it up. Cancelling there stops it before
  anything is touched. For artifact-backed gallery models, cancelling an active
  download leaves its partial files in place so a later install resumes rather
  than starting over. A cancelled operation leaves the live sections;
  it appears in the record as `cancelled`.

  **Cancel waits for 8 seconds before it is sent.** The row says "Cancelling
  unless you undo" and a toast offers **Undo**. The server cannot take a cancel
  back, so the wait in the browser is the whole undo: while it runs nothing has
  been stopped and the download carries on. Closing the toast cancels at once,
  asking to cancel a second download ends the first one's wait, and leaving the
  page sends a cancel that is still waiting. A job that finishes during the wait
  is left alone.
- **Retry** is offered on a failed install or update. It acknowledges the
  failure, which moves it into the record, and starts the same work again: an
  install again, or an update again for a failed backend update (on one node,
  for a node-scoped one). It is not offered on a failed removal, which is not
  restarted by reinstalling, or on a failed staging job.
- **Dismiss** acknowledges the failure without
  retrying. The operation moves into the record with a `failed` outcome; it is
  not deleted. This is why the same failure can be found either under **Needs
  attention** or in the **Record**, depending on whether it has been
  acknowledged.

{{% callout style="note" %}}
Retrying a model that was installed with an explicit `variant` reinstalls it
with automatic variant selection, because the variant is not carried on the
operation. If you pinned a build, reinstall it from the Models page or the API
with the `variant` you want rather than using **Retry**. See
[Model Gallery]({{% relref "/models" %}}) for variants.
{{% /callout %}}

## History

The record holds the last 50 finished operations. Where it is kept depends on
how LocalAI is running:

- **Standalone**: in memory, so it is empty again after a restart. For durable
  per-model install state, use the model and backend listings rather than this
  page.
- **[Distributed mode]({{% relref "/administration/distributed-mode" %}})**: in
  PostgreSQL, alongside the operations themselves. It is the same record on
  every replica, it survives restarts, and a replica added by a scale-out or a
  rolling deploy reports it in full rather than starting empty.

**Clear history**, beside the page title whenever the record has anything in
it, empties it. Running operations and unacknowledged failures are untouched.
In distributed mode it clears the record for every replica, not just the one
serving the page.

## The sidebar count

The **Operate** entry in the sidebar carries a badge whenever something is in
flight, showing how many operations are running or queued. If any failure is
waiting to be acknowledged, the badge turns red and counts the failures
instead, so the number always reports the thing that most wants your attention.

## API

Both surfaces are backed by these endpoints. All of them are admin-only when
[authentication]({{% relref "/administration/authentication" %}}) is enabled.

| Method   | Path                             | Description                                                              |
| -------- | -------------------------------- | ------------------------------------------------------------------------ |
| `GET`    | `/api/operations`                | Running, queued and failed operations, least advanced first. `taskType` is `installation`, `upgrade`, `deletion` or `staging`. |
| `POST`   | `/api/operations/{jobID}/cancel` | Cancel a queued operation, or a running install. Not a running removal.  |
| `POST`   | `/api/operations/{jobID}/pause`  | Pause a queued operation or running download and preserve partial data.  |
| `POST`   | `/api/operations/{jobID}/dismiss`| Acknowledge a failed operation and move it into the record.               |
| `GET`    | `/api/operations/history`        | List finished operations, newest first.                                   |
| `DELETE` | `/api/operations/history`        | Clear the record. Live operations are untouched.                          |

Pausing preserves the download's `.partial` file. Start the same model or
backend installation again to resume from the saved bytes when the origin
supports HTTP Range requests. Cancelling intentionally discards partial data.

Both `GET` endpoints wrap their list in an `operations` key rather than
returning a bare array:

```bash
# What has finished
curl http://localhost:8080/api/operations/history \
  -H "Authorization: Bearer <admin-key>"
```

```json
{
  "operations": [
    {
      "id": "localai@qwen3-4b",
      "name": "qwen3-4b",
      "jobID": "6f1b0c2e-...",
      "isBackend": false,
      "taskType": "installation",
      "outcome": "completed",
      "startedAt": "2026-07-27T10:02:11.482913574Z",
      "finishedAt": "2026-07-27T10:03:23.117402881Z"
    }
  ]
}
```

`nodeID` is present when the operation was scoped to a worker, and `error`
when the `outcome` is `failed`. The `outcome` is one of `completed`, `failed`
or `cancelled`. The `taskType` is `installation`, `deletion` or, for a backend
update, `upgrade`.

```bash
# Forget the record
curl -X DELETE http://localhost:8080/api/operations/history \
  -H "Authorization: Bearer <admin-key>"
```

The `DELETE` answers `500` with an `error` if the record could not be cleared,
which in distributed mode means the rows are still there. It never reports
success on a record it did not clear.
