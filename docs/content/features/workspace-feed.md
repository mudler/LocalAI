+++
disableToc = false
title = "Chats: the change feed of your files"
weight = 24
url = "/features/workspace-feed/"
+++

The change feed tells a client which of your files changed, as it happens. A
change can come from anywhere: you in another tab, a chat that wrote a file
with a tool, an upload, a trigger run, a connector, another API client, or
another frontend of a cluster. The web UI uses it to refresh what it shows
without a reload.

The files are the workspace of the [chats](/features/chats/), so the feed
needs the `chats` feature, which is on by default. When the server has no
files (no data path), the stream answers `409` with the type `files_off`.

## Open the stream

```bash
curl -N http://localhost:8080/api/workspace/events \
  -H "Authorization: Bearer $API_KEY"
```

The answer is a stream of
[server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html):

```text
id: 42
event: change
data: {"revision":42,"paths":["LocalAI/home.json","pages/tasks/index.html"],"kinds":["agent"]}

: ping

id: 57
event: resync
data: {"revision":57}
```

| Event | Data | Meaning |
|---|---|---|
| `change` | `revision`, `paths`, `kinds` | Files in your home folder changed. |
| `resync` | `revision` | Some changes were left out. Read again everything you show. |

- `paths` are inside your home folder, without a leading slash
  (`pages/tasks/index.html`). A move gives the old and the new path. A
  folder that a write created is a path too. You never see a path you cannot
  read, and never a path of another user.
- `kinds` are the kinds of the change sets: `user` (a change you made in the
  UI or the API), `agent` (a chat), `upload` and `undo`.
- `revision` is the revision of the newest change in the event. It is also the
  event id.
- Changes that arrive close together come as one event. The feed reads the
  record of changes about once a second, so expect up to a second of delay.
- A comment line (`: ping`) comes every 25 seconds, so that idle proxies keep
  the connection open.

## Resume after a disconnect

Without a resume point the stream starts from now. To get the changes you
missed, send the last event id back: a browser `EventSource` sends it by itself
as the `Last-Event-ID` header when it reconnects, and a client that opens a new
stream can send `?after=<revision>`.

```bash
curl -N "http://localhost:8080/api/workspace/events?after=42" \
  -H "Authorization: Bearer $API_KEY"
```

When more than 500 changes to your files have happened since that revision, the stream
starts with a `resync` event instead of the changes.

## Limits

- At most 8 open streams per user. One more answers `429`. The web UI opens one
  stream per browser tab, whatever the number of pages that listen.
- A client that does not read fast enough does not slow anything down: when
  its queue is full, the queued events are replaced by one `resync` event.

## Several frontends

The feed keeps no state that frontends must share. Every change to a file is
written, with a revision, to the record of changes of the workspace (the
journal), and revisions are committed in order. In a cluster the journal is in
the shared database, so each frontend reads the same record, and a change that
any process made reaches the clients of every frontend. Nothing goes over the
message bus: the bus delivers a message at most once, the journal does not
lose one.

Each frontend reads the journal only while at least one client is connected to
it: one small query per second, and one query per user whose files changed.
The position in the journal of each frontend and of each client is kept in that
process only. After a restart it starts again from the newest revision, and a
client that reconnects gives its own position back.

A server without a database keeps the journal in a folder (the JSON index).
That server runs alone, so the feed is local too.

## In the web UI

Pages of the React UI listen through one shared stream per tab
(`useWorkspaceFeed`). They also refresh when the window gets the focus again
and when a chat turn ends, in case the stream was down. The **Files** page
reads the open folder again when something in it changes, so a file that a
chat or another tab writes shows up without a reload.
