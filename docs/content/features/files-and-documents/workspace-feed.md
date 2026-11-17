+++
url = "/features/workspace-feed/"
disableToc = false
title = "Files change events (API)"
weight = 40
devbuild = "feature"
+++

Open one HTTP stream to learn when your files change. Use it to refresh a view or trigger a script when a chat, an upload or another tab changes a file. A change can come from you in another tab, a chat that wrote a file with a tool, an upload, a trigger run, a connector, another API client, or another frontend of a cluster. The web UI uses the same stream to refresh without a reload. To undo a change, use **History** in [Files]({{% relref "/features/files-and-documents/files" %}}).

The feed is part of Chats. It is on by default. When the server has no files (no data path), the stream answers `409` with the type `files_off`.

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
- `kinds` say where the change came from: `user` (a change you made in the
  UI or the API), `agent` (a chat), `upload` and `undo`.
- `revision` is the revision of the newest change in the event. It is also the
  event id.
- Changes that arrive close together come as one event. The feed reads the
  changes about once a second, so expect up to a second of delay.
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
- A slow client gets a `resync` event and reads again. It doesn't slow anything down.

## In a cluster

Every frontend reads the same shared record of changes, so a change that any process made reaches the clients of every frontend. A server without a database keeps the record in a folder and runs alone. After a restart a frontend starts again from the newest revision, and a client that reconnects gives its own position back.

## In the web UI

The **Files** page reads the open folder again when something in it changes, so a file that a chat or another tab writes shows up without a reload. Pages also refresh when the window gets the focus again and when a chat turn ends, in case the stream was down.
