+++
disableToc = false
title = "Activity"
weight = 21
url = "/features/activity/"
description = "Watch, cancel and retry model and backend installs from the WebUI"
+++

Model installs, backend installs, removals and cluster staging all run in the
background. Two surfaces in the WebUI report them: a one-line strip at the top
of the app, and the **Activity** page, which holds the full picture.

Both are admin-only.

## The operations strip

While there is anything to report, a single line appears at the top of the app.
It shows one operation at a time:

- a failure, if there is one, because an error waiting for a decision outranks
  any progress;
- otherwise the least advanced running operation, which is the one everything
  else is waiting behind.

Every operation shows what is being done and to what, for example
`Installing model qwen3-4b`. Everything else depends on what the operation can
report. A percentage and its bar appear only once the operation is running and
has reported progress, so a queued operation, a failed one and a removal show
none. Artifact-backed gallery models report two things more: the phase
(`Resolving files`, `Downloading`, `Verifying`, `Finalizing`, `Saving
configuration`) and downloaded/total bytes. A backend installing on several
workers is rolled up into one phrase, `2 of 5 nodes done`; the per-node
breakdown lives on the Activity page.

When more than one operation is in flight, a **N more** counter on the right
links to the Activity page.

When the last operation finishes, it stays on the strip for about four seconds
and then goes, so a fast install is not a flicker. While others are still
running, the strip moves straight on to the next one. An operation you cancel
is not held: it goes as soon as it stops.

The **X** on the right hides the strip. It does not cancel anything: the work
carries on, the sidebar still counts it, and the Activity page still lists it.
Hiding applies to that one operation, so a later failure brings the strip back.
On a failure the same button acknowledges the error and moves it into the
record instead.

## The Activity page

**Operate → Activity** (`/app/activity`). The page has up to three sections,
each shown only when it has something in it.

### In progress

One card per running or queued operation, each naming what is being done and to
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
each card carrying the error returned by the installer. Cluster staging never
appears here: a staging job reports no error to the page, so a staging failure
has to be read from the logs.

### Record

What finished, newest first, one row each: the name, what happened
(`installed in 1m 12s`, `removed`, `cancelled`, or `failed:` with the error),
the time of day it finished, and a link into Models or Backends. Model and
backend installs and removals are recorded; cluster staging is not, so a
staging run leaves nothing behind here once it finishes.

### Filters

Four chips above the sections filter the whole page: **All**, **Models**,
**Backends** and **Cluster**. In the live sections, Cluster covers staged model
files and any install that involves workers, whether it targets one node or
fans out across several. In the Record it covers only the node-targeted case: a
finished fan-out install is filed under Models or Backends, not under Cluster.

## Cancelling, retrying and dismissing

These are on the operation cards. **Cancel** and **Retry** are labelled
buttons; dismissing is the **X** at the end of a failed card. The strip has no
cancel button; the page is the only place work is stopped or restarted.

- **Cancel** is offered while an operation is queued, whatever it is, and while
  an install is running. It is not offered once a removal has started: a
  removal in progress cannot be interrupted, so the window to call one off is
  the queue, before a worker picks it up. Cancelling there stops it before
  anything is touched. For artifact-backed gallery models, cancelling an active
  download leaves its partial files in place so a later install resumes rather
  than starting over. A cancelled operation leaves the live sections
  immediately and is not held on the strip the way a completed one is; it
  appears in the record as `cancelled`.
- **Retry** is offered on a failed model or backend install. It acknowledges
  the failure, which moves it into the record, and installs the same target
  again. It is not offered on a failed removal, which is not restarted by
  reinstalling.
- **Dismiss**, the **X** on a failed card, acknowledges the failure without
  retrying. The operation moves into the record with a `failed` outcome; it is
  not deleted. This is why the same failure can be found either under **Needs
  attention** or in the **Record**, depending on whether it has been
  acknowledged.

{{% notice note %}}
Retrying a model that was installed with an explicit `variant` reinstalls it
with automatic variant selection, because the variant is not carried on the
operation. If you pinned a build, reinstall it from the Models page or the API
with the `variant` you want rather than using **Retry**. See
[Model Gallery]({{% relref "features/model-gallery" %}}) for variants.
{{% /notice %}}

## History

The record holds the last 50 finished operations. Where it is kept depends on
how LocalAI is running:

- **Standalone**: in memory, so it is empty again after a restart. For durable
  per-model install state, use the model and backend listings rather than this
  page.
- **[Distributed mode]({{% relref "features/distributed-mode" %}})**: in
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
[authentication]({{% relref "features/authentication" %}}) is enabled.

| Method   | Path                             | Description                                                              |
| -------- | -------------------------------- | ------------------------------------------------------------------------ |
| `GET`    | `/api/operations`                | Running, queued and failed operations, least advanced first.              |
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
or `cancelled`.

```bash
# Forget the record
curl -X DELETE http://localhost:8080/api/operations/history \
  -H "Authorization: Bearer <admin-key>"
```

The `DELETE` answers `500` with an `error` if the record could not be cleared,
which in distributed mode means the rows are still there. It never reports
success on a record it did not clear.
