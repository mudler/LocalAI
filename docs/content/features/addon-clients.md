+++
disableToc = false
title = "Paired clients: inference and add-ons"
weight = 26
url = "/features/addon-clients/"
+++

A **paired client** is any program or box that is not a person in a browser and
that the owner paired with a LocalAI instance: a small always-on unit with its
own front end, a script, a service. One pairing gives the client one key, and
the key can do two things:

- **Inference.** The client calls the standard OpenAI-style API with its own key,
  for the models that the owner chose. See [Inference with a client
  key](#inference-with-a-client-key).
- **Add-on calls.** The client talks to an [add-on](/features/addons/) that the
  owner installed. The rest of this page is for people who write that add-on. It
  describes the routes of the client, the event stream and the host operations.

For add-on calls, the add-on does the work. LocalAI checks who the client is, what
it may reach and how much it may send, and forwards the request.

## Pair a client

The owner makes a one-time pairing code, and the client sends it to LocalAI.
Pairing needs sign-in to be on. With sign-in off every caller is already the
local owner, so a client credential would add nothing and the routes answer
`409`.

| Route | Who | What it does |
|---|---|---|
| `POST /api/client/pairing-codes` | administrator | Makes a code. Body: `name`, `features` (a list of client features), `addons` (the ids of the add-ons the client may reach; see below), `models` (the names of the models the client may use; see below), `readFolders` and `writeFolder` (the folder grant, see [Folders of the owner](#folders-of-the-owner)), `expiresInMinutes`, `keyExpiresIn` (for example `90d`). Returns the code once. When LocalAI serves TLS, the answer also has `tlsFingerprint` and `tlsDigest` (see [TLS and the pinned certificate](#tls-and-the-pinned-certificate)). |
| `GET /api/client/pairing-codes` | administrator | Lists the codes that are not used or expired, with the same `tlsFingerprint` and `tlsDigest` when TLS is on. |
| `DELETE /api/client/pairing-codes/{id}` | administrator | Cancels a code. |
| `POST /api/client/pair` | the client, no credential | Body: `code`, optional `name`. Returns the client key once. |
| `GET /api/client/keys` | administrator | Lists the paired clients, with their features, their add-ons, their models and last use. |
| `PUT /api/client/keys/{id}` | administrator | Changes the add-ons, the models or the folder grant of a paired client. Body: `{"addons": ["id", ...]}`, `{"models": ["name", ...]}`, `{"readFolders": [...], "writeFolder": "..."}` or any of them. A field that is absent stays as it is. It applies at the next request. An empty add-on list cuts the client off from every add-on. A key with `client_inference` needs at least one model. |
| `DELETE /api/client/keys/{id}` | administrator | Revokes a client. It stops at the next request, and it closes a realtime session that is open (see below). |
| `GET /api/client/self` | the client | Returns what the key may use in one call: `id`, `name`, `features`, `addons`, `models`, the expiry, and the `version` and `commit` of the instance. `version` is the tag of a release, or `dev` for a source build. Needs `client_self`. |

A code has 10 characters, for example `7KQ3M-X9PTD`. It expires after 10 minutes
by default and after one hour at most. It works once. LocalAI stores only its
hash. An owner can hold 5 unused codes. The pairing route allows 10 tries a
minute for each address. There is no limit for the whole instance, so nobody can
block the pairing of other owners. A text that is not shaped like a code gets
`400` and does not count as a try. A wrong, an expired and a used code get the
same answer.

### What a client key can do

A client key is a row of the API key table with the kind `client`. It is not a
user key. It has the role of an ordinary user, even if an administrator made the
code. It can reach only the routes that name one of the features of its list,
and it is refused everywhere else, also on routes that need no special feature.
A client key cannot start a session, create other keys or reach the add-on
routes of the web UI.

| Client feature | Routes |
|---|---|
| `client_self` | `GET /api/client/self` |
| `client_addons` | `/api/client/addons/{addon}/...`, for the add-ons in the list of the key (see below) |
| `client_inference` | The seven inference routes below, for the models in the list of the key |
| `client_folders` | No route. It switches on the folder grant of the key (`readFolders`, `writeFolder`), which the add-on reaches with the `client.workspace.*` host operations (see [Folders of the owner](#folders-of-the-owner)) |

A code without a list gets `client_self`. A key with only `client_inference`
cannot read `GET /api/client/self`: add `client_self` when the client should
learn its models and the version of the instance.

### Which add-ons a client can reach

A key with `client_addons` reaches only the add-ons that the owner chose. The
code carries the list in `addons` (at most 16 ids). The list is required when
`client_addons` is in `features`, and it is refused when it is not, so every
code that opens add-ons says which ones. Each id must be a well-formed add-on
id. LocalAI does not check that the add-on is installed, because it can be
installed after the pairing. The list is copied to the key when the client
pairs. `GET /api/client/pairing-codes`, `GET /api/client/keys` and
`GET /api/client/self` show it.

The rule is deny by default. A request to `/api/client/addons/{addon}/...` for an
add-on that is not in the list of the key gets `403`. The answer is the same
when the add-on does not exist, so a client cannot learn which add-ons are
installed. A key with `client_addons` and an empty list reaches nothing. The
owner can change the list of a paired client with `PUT /api/client/keys/{id}`;
the client does not have to pair again, and it cannot change its own list.

Client features are not user permissions. They do not appear in the user
settings.

## Inference with a client key

A key with `client_inference` calls these routes of the standard API, with
`Authorization: Bearer <client key>`, and no others:

| Route | Use |
|---|---|
| `POST /v1/chat/completions` | Chat |
| `POST /v1/audio/transcriptions` | Speech to text |
| `POST /tts` and `POST /v1/audio/speech` | Text to speech |
| `POST /v1/audio/classification` | Sound classification |
| `GET /v1/realtime` | Realtime session over a websocket |
| `GET /v1/models` | The models of the key |

The list is fixed. Embeddings, voice activity detection, voice and face
recognition, diarization, sound generation, audio transformations, MCP, the
responses API, realtime calls and sessions, and every admin route are refused to
a client key, also when the owner could use them. A route that is added to LocalAI
later is not added to this list by itself: a test freezes it.

### Which models a client can use

The key carries a list of model names, like the list of add-ons. The code
carries it in `models` (at most 32 names). It is required when `client_inference`
is in `features`, and it is refused when it is not. It is copied to the key when
the client pairs, and the owner changes it with `PUT /api/client/keys/{id}`.

The rule is deny by default:

- A request must name a model, and the model must be in the list. The check runs
  in the handler, on the name that the handler resolved from the request, so
  the model that runs is the model that was checked. An earlier check reads the
  path, the query, a JSON body and a form field, and it refuses a request that it
  cannot read without doubt: a body that is not JSON, a urlencoded form or a
  multipart form (`415`), data after the JSON value, a model field twice or in
  another case, or a model given twice in the query (`400`). A request that names
  no model gets `400`: a client key has no default model. A model that is not in
  the list gets `403`.
- `GET /v1/models` names no model. It answers with the models of the list that
  the key can still use.
- For a realtime session, the list is checked for the pipeline that the client
  connects with (`?model=`). The stages of the pipeline are not checked one by one.
  A `session.update` that names another pipeline or another transcription model
  is refused, so the session stays on the model it was checked for.
- A model on the list must exist (it has a model configuration), and it must have
  **no tools attached**: no MCP servers, no automatic MCP, no agent settings, and
  the same for the models that an alias, a failover chain, a router (its
  candidates and its fallback) or a pipeline hands the
  request to. A model with tools would run them under the account of the owner. The
  owner gets `400` when a listed model does not fit. The check runs again on every
  request, so a model that gains tools after the pairing is refused with `403`
  and is dropped from `GET /v1/models`.

A client key never sees the saved voice profiles of the owner. A voice profile
reference answers `404`, also for a profile that is shared with the whole
instance, and a voice that is a path is refused. A voice of the model itself works.
A pipeline whose own configuration names a saved profile as its voice cannot
start for a client.

The chat and transcription requests of a client key are recorded in the usage
of the owner, with the id of the key. The other routes (speech, `/tts`, sound
classification and realtime sessions) are not recorded in the usage yet. The user
quota does not apply to a client key, and neither do the feature switches and the
model allow-list of the owner account: a key works only while its owner is an
active administrator, so a demoted owner stops its clients. There is no limit per
key on the number of requests or on how many run at once; that is planned.

### A realtime session ends when the key is lost

A realtime session lasts minutes. LocalAI asks every 5 seconds whether the key
still stands, and it sends an error event (`client_key_invalid`) and closes the
connection when the key is revoked, has expired or is paused, when the owner
account is not active or is no longer an administrator, when the model of the
session left the list of the key, or when that model gained tools. A change
of the list therefore reaches an open session within about 5 seconds, and a
new request at once. A check that fails (the store errors, or does not answer
within 5 seconds) does not close the session at once: it closes after three
failed checks in a row.

### TLS and the pinned certificate

A pairing code and a client key cross the network, so a client should pair over
TLS. LocalAI serves TLS when it is started with a certificate (`--tls-cert` and
`--tls-key`) or with `--tls-self-signed`; see the [CLI
reference]({{%relref "reference/cli-reference" %}}). It is off by default, and TLS
1.3 is the minimum.

With `--tls-self-signed`, LocalAI makes a certificate once, keeps it in the
`tls` folder of the data path and logs its SHA-256 fingerprint at each start. The
answers that make and list pairing codes carry that fingerprint (`tlsFingerprint`,
64 hex digits) and a short `tlsDigest` of the first 8 digits in two groups, for
example `AB12-CD34`. The owner can read the digest to the person at the client,
or put the fingerprint in the pairing offer. The client **pins** the certificate:
it accepts only a certificate with that fingerprint, before it sends the code. A
self-signed certificate is then as strong as any other for that client, since the
names inside it do not matter.

If LocalAI runs behind a proxy that ends TLS, the answers have no fingerprint,
because LocalAI does not know the certificate of the proxy.

## The routes of an add-on

The add-on declares the routes in `manifest.json`, under `capabilities.client`.
This needs `featureLevel` 7 (8 when a route uses a wildcard tail) and
`capabilities.service`.

```json
{
  "featureLevel": 7,
  "capabilities": {
    "service": true,
    "hostOperations": { "required": ["client.jobs.start", "client.jobs.get", "client.jobs.cancel"] },
    "client": {
      "routes": [
        { "id": "start",  "method": "POST", "path": "/jobs", "maxBodyBytes": 4096 },
        { "id": "job",    "method": "GET",  "path": "/jobs/{id}" },
        { "id": "events", "method": "GET",  "path": "/events", "stream": true }
      ]
    }
  }
}
```

| Field | Meaning |
|---|---|
| `id` | Names the route in the request. Lower case letters, digits and dashes. |
| `method` | `GET`, `POST`, `PUT`, `PATCH` or `DELETE`. |
| `path` | Starts with `/`. A segment is lower case letters, digits, `-` or `_`, or `{name}` for one variable segment. The last segment may be `{name...}`, a wildcard tail (see below). At most 6 segments and 128 characters. No dot, no escape, no empty segment. |
| `stream` | `true` for a server-sent event stream. The method must be `GET`. |
| `maxBodyBytes` | The largest request body. Default 65536, at most 1048576. A `GET` or `DELETE` takes no body. |
| `timeoutSeconds` | The time one call may run. Default 10, at most 60. Not for a stream. |

At most 32 routes. Two routes that match the same request are refused.

### A wildcard tail

The last segment of a path may be `{name...}`. It matches **one or more**
segments, so `/files/{path...}` serves `/files/notes.md` and
`/files/2026/11/trip.md`, and not `/files`. This needs `featureLevel` 8. A host
of level 7 refuses the manifest with the plain reason "needs a newer LocalAI",
and LocalAI refuses a manifest that uses a tail and declares a lower level.

- Each matched part follows the rule of a variable segment: 1 to 128 characters
  from letters, digits, `_`, `.`, `~`, `:` and `-`. A part that is `.` or `..`,
  an empty part, a percent escape, a backslash and a space match nothing. This
  is the same rule as for `{name}`, applied to every part.
- The add-on gets the parts as one parameter, joined with `/`: for
  `/files/2026/11/trip.md` the parameter `path` is `2026/11/trip.md`.
  **The add-on must validate it itself** before it uses it as a path of its own.
  The host cannot know what the add-on does with the value. It guarantees only
  the rules above, so a name such as `.hidden` or `a.b` still arrives, and a
  link or a long path is for the add-on to refuse.
- A tail has no name clash: a name may be used once in a path, tail included.
- A route without a tail wins over a route with one, whatever the order in the
  manifest, so `/files/index` beats `/files/{path...}` for `/files/index`. Between
  two routes with a tail, the one with the longer fixed part wins. The rule is
  applied for each method: a route that does not tail only shadows the tail
  routes of its own method. With `PUT /files/index` and `GET /files/{path...}`,
  a `GET` of `/files/index` reaches the tail, and a `PUT` reaches the literal
  route. Two routes of the same method and shape (`/a/{x...}` and `/a/{y...}`) are a manifest error.
- Limits: a route with a tail takes a request path of at most 256 characters and
  12 segments, since a path to a file needs more room than a fixed route. A
  route without a tail keeps 128 characters and 6 segments. The declared path
  keeps 6 segments and 128 characters, tail included.

A client calls `/api/client/addons/{addon id}{path}` with its key. LocalAI
forwards the request only if the method and the path match a declared route:

- The path is matched as the client sent it. A percent escape, a backslash, a
  dot segment, an empty segment and a trailing slash match nothing.
- The body is read with the limit of the route, and it must be
  `application/json`. A larger body gets `413`.
- A wrong method gets `405`. An unknown route gets `404`.
- A client has at most 8 calls and 2 streams at once, and 240 calls a minute.
- The request goes over the loopback connection of the add-on with the
  per-start bearer. The client never sees an address, and it cannot choose one.

The add-on gets a `ClientRequest` with the route id, the method, the path, the
variables, up to 16 query values, the body, the `Last-Event-ID` and the client:

```json
{ "operationId": "cl-9f2c...", "routeId": "job", "method": "GET", "path": "/jobs/ab12",
  "params": { "id": "ab12" }, "client": { "id": "<client key id>", "name": "kitchen" } }
```

The add-on never gets the key of the client. It gets no user id and no role.

The add-on answers with a status, a content type and a body. The status must be
one of 200, 201, 202, 204, 400, 404, 409, 410, 413, 422 or 429. The content type
must be `application/json` or `text/plain; charset=utf-8`. The body is at most
1 MiB. LocalAI passes no other header of the add-on. Another status or a failed
call becomes a `502`, `503` or `504`.

The runtime protocol has two new routes, `POST /client` and `POST /client/stream`.
An add-on built with the SDK of this repository implements
`contract.ClientRuntime`. The reference add-on is in
`addons/internal/clientdemo`.

## The event stream

A route with `"stream": true` answers with `text/event-stream`. LocalAI:

- sends the headers at once and flushes every chunk, and turns off gzip for the
  path;
- writes a comment line (`: hb`) every 15 seconds when the stream is quiet, never
  in the middle of an event;
- ends the upstream stream when the client goes away;
- ends the stream after one hour, and when the client key is revoked, expired or
  paused (checked at each heartbeat).

The add-on keeps the outbox. A good outbox gives each event an `id` that only
grows, also after a restart, and an expiry. When a client reconnects, it sends
the last id it saw in `Last-Event-ID`, and the add-on sends what came after it.
If events are gone, send one `notice` event so that the client asks the state of
its jobs. The reference add-on uses the events `job.progress`, `job.done`,
`job.failed` and `notice`, keeps at most 100 events for each client for one hour,
and watches at most 8 jobs for each client.

## Host operations for jobs

A job of a client runs a **task** that the owner saved (see
[Agent tasks and jobs](/features/agent-jobs/)). The add-on names the task and
fills its parameters. It cannot send a prompt of its own.

| Operation | Arguments | Result |
|---|---|---|
| `client.jobs.start` | `task` (name), `params` (up to 16, value up to 4096 characters) | the job: `id`, `status`, `task` |
| `client.jobs.get` | `id` | the job, with `result` (up to 16 KiB) and `error` |
| `client.jobs.cancel` | `id` | the job |

The owner must grant the add-on the operations as for any host operation (for
example the prefix `client.jobs.` in the host policy), and must hold the
`mcp_jobs` feature. Prefer the narrow prefix: the prefix `client.` also covers
`client.list` and `client.workspace.*`, and so gives the add-on the files of
the client folders.

The client is not an argument. LocalAI records each forwarded call under the
owner and the client key. The add-on passes the `operationId` of the call to the
host with each operation, and LocalAI finds the client in its own record. During
a call that a client made, the add-on can use the operations that this page names
for a client call and no others: the three job operations, and the three folder
operations of [Folders of the owner](#folders-of-the-owner). The host checks the
exact name, so a new operation under `client.` stays closed to a client call
until it is named. A stream call lasts as long as the stream, so the add-on can ask for the
state of a job while the client is connected. Each operation checks that the key
is still paired.

A client sees only the jobs that were started for it. It may run 3 jobs at once.
Ended jobs do not count against this limit.

### Which clients an add-on can reach

An add-on that wants to address one client (for example to prepare a file for
it) asks which clients exist. The operation is not for a client call: the add-on
uses it while it works for the owner.

| Operation | Arguments | Result |
|---|---|---|
| `client.list` | none | `{"clients": [{"id": "...", "name": "..."}]}` |

The host decides who is in the list, not the add-on:

- Only clients of the owner of the call. The add-on is the one that made the
  call, and an argument cannot name another add-on, another owner or a client.
- Only clients that may reach this add-on (they hold `client_addons` and the
  add-on is in their list) and that stand now: not revoked, not expired, not
  paused, with an owner that is still an active administrator.
- Only the `id` and the `name` (printable characters, at most 64 bytes, as in
  `ClientRequest`). No key, no hash and no feature list.

The call needs the owner to grant the add-on `client.list` (the prefix
`client.list` is enough, and narrower than `client.`), and the actor must be an administrator. Otherwise the add-on
gets the same refusal as for any host operation it does not hold. A call that
a client made never reaches `client.list`: during such a call the add-on can use
only the job and folder operations of this page, by exact name and not by
prefix.

### Folders of the owner

A paired client may work with a few folders of the owner's files, through an
add-on. The owner decides which, per client. The add-on never decides, and the
home of the owner is never the rule.

**The grant.** A client key carries `readFolders` (up to 8) and `writeFolder`
(at most one). Both are paths relative to the owner's files, for example
`Notes` or `Memory/agents/kitchen`. The owner sets them when it makes the
pairing code, together with the feature `client_folders`, and changes them
later with `PUT /api/client/keys/{id}` (`{"readFolders": [...], "writeFolder":
"..."}`; an empty list or an empty string takes the access away). Only an
administrator can do this, and the client cannot change its own grant. Empty
means no access. The feature and the grant go together: a code with
`client_folders` needs at least one folder, and a code with a folder needs the
feature. A key can read its write folder as well.

A folder name is refused when it is an absolute path, has a `..` or `.`
segment, an empty segment (so no trailing slash), a NUL or other control
character, or a backslash. It names at least one folder, so no grant covers all
of the files.

| Operation | Arguments | Result |
|---|---|---|
| `client.workspace.list` | `path` (a folder; `""` lists the granted folders) | `entries` with `name`, `dir` and `size` |
| `client.workspace.read` | `path` (a file) | `path`, `size`, `content` |
| `client.workspace.write` | `path` (a file), `content` | `path`, `bytes`, `sha256`, `revision` |

Rules the host enforces, for each call:

- The key is read from the store at each call. A key that is revoked, expired or
  paused, whose owner is no longer an active administrator, that may not reach
  the add-on, or that has no grant gets one generic refusal. The same refusal
  answers an add-on that was not granted the operations, an owner without
  the files feature and a call that no client made.
- A path must lie inside a folder of the grant. `list` and `read` take the read
  folders and the write folder, and `write` takes the write folder only. A write
  to a read-only folder, to a parent or to a sibling of the write folder is
  refused. The path is checked by the host before the store sees it, and the
  store holds the call to the one granted folder as well. The owner's files are
  a versioned store, not a directory on disk, so there is no link to follow.
- Text formats only: `.md`, `.txt` and `.json`. A `.json` file must be valid
  JSON. `list` shows these files and folders only.
- Names below the write folder use letters, digits, `_`, `.`, `~` and `-`, no
  leading dot, 128 characters per name, 16 segments per path.
- Sizes: `read` returns a file of at most 512 KiB, and an answer that would be
  over the 1 MiB payload of a host operation once it is JSON-encoded (a file
  full of `<` takes six bytes a character) is refused. `list` returns at most
  200 entries a page, sorted by name; when more follow, the answer has
  `"truncated": true` and `next`, which the add-on passes back as `after`. `write` takes a file of at most
  256 KiB and keeps at most 50 MiB in the write folder; replacing a file counts
  the new size only.
- A write is one change in the history of the owner's files, made as the owner,
  with the title "Paired client <id> wrote <name>", so the owner can tell which
  client wrote a file and undo it. The write folder is counted once and the
  count is kept up to date by the writes (it is counted again after 30 seconds),
  with at most 4096 files and folders in it. Writes to one folder run one at a
  time; a write that waits reads the grant again before it runs, so a key that is
  revoked, paused or narrowed meanwhile does not complete it.
- **Audit.** The host logs each write with the add-on, the id of the client,
  the path, the byte count and the SHA-256 of the content, and each refused
  operation with its reason, at the info level with the other host operation
  logs. The log never holds the content or a credential. The reason of a
  refusal on the grant (a key that is revoked, paused, without a grant, or a path
  outside the folders) is not sent to the add-on: it gets the generic refusal.
  A refusal about the request itself is sent, in fixed words and with no path: a
  file type that is not allowed, an invalid file name, a file or answer that is
  too large, a folder that is full or has too many files, content that is not
  text or not valid JSON.

**The envelope of a client call.** A client call is recorded with the read
scope. A call on a route that changes things (any method except `GET`) is
recorded with the apply scope, which `client.workspace.write` needs. Streams and
`GET` routes keep the read scope, and no client call gets the act scope. Apply
does not widen what a client call may do: the broker allows a client call only
the operations it names, by exact name, and these are the job operations and the
three folder operations. Declare the folder operations in `hostOperations` and
ask the owner for the prefix `client.workspace.`, not for `client.`: that prefix
also covers `client.list`, and every operation added under it later.

**Example: a write folder that the memory loader reads.** The built-in memory
reads `Memory/index.md` and, next to each index, topic files that the model
opens by name (`core/services/studio/memory.go` lists the three indexes,
`core/services/studio/contextfiles/loader.go` reads `<index folder>/<topic>.md`).
The folders with an index are `Memory/`, `Memory/agents/<agent>/` and
`Memory/channels/<channel>/`. A write folder inside one of them needs no
proposal: the files are read as the owner's own notes.

**Security: this lets a paired client plant text that the owner's chats load.**
What a client writes there is read like a note that the owner or the assistant
saved. A client that is compromised, or an add-on that passes on what a person
told it, can write text that steers the chats that read the folder. Memory
notes come in a block that the prompt marks as saved data, but the model still
reads it. Give such a folder only to a client that you trust as much as your own
notes, keep it to one saved agent, and look at the history of the files, where
each write has the client id in its title.

```json
{ "name": "kitchen", "features": ["client_self", "client_addons", "client_folders"],
  "addons": ["notes-sync"], "writeFolder": "Memory/agents/kitchen-helper" }
```

The add-on saves `PUT /files/Memory/agents/kitchen-helper/pantry.md` (the demo
route `/files/{path...}`), and chats of the saved agent `kitchen-helper` can
open the topic `pantry`. Three limits to know. A topic name is letters, digits,
`_` and `-` (at most 64), so a file named `pantry.v2.md` is stored but never
loaded as a topic. A folder deeper than the one with the index, such as
`Memory/clients/kitchen`, is stored but not read by the loader. And a write
folder that holds `index.md` lets the client replace that index: give the
client a folder of its own, or name the topics yourself.

### Which tools a job can use

A job that is started for a client **reads and reports only** by default. A tool
that writes or acts gets a plain refusal as its result, also when the agent of
the task holds the grant.

The owner can lift this with the setting `client_jobs_allow_all_tools`
(`POST /api/settings`). It is off by default. With it on, a client job may use
the tools that write or act, within the grants of the agent of its task: the
same grants an ordinary job of that task has. It never gets a grant that the
agent does not hold, so a task without an agent still cannot write or act. The
setting applies to jobs that are already running. It does not change the jobs
that other people start.

## Limits to know

- **All limits are per process.** The tries of the pairing route, the calls and
  streams of a client, and the rate of a client are counted in the memory of one
  LocalAI process. With several frontends, each one has its own count, so the
  limits multiply. The job quota of a client is shared (it is counted in the
  job store, under a lock that all frontends share), and so is the limit of 32
  paired clients. `POST /api/client/pair` works on any frontend, while the
  routes of an add-on work only on the instance that owns the add-ons.
- **A client stream uses one of the 64 long calls of the add-on.** The same pool
  serves the connector polls of the add-on, so 32 clients with 2 streams each
  fill it. Keep the number of paired clients of such an add-on low, or lower
  `MaxStreamsPerClient`.
- **Client calls are capped inside the declared concurrency.** A call to a route
  that does not stream may run for up to a minute, so all clients together may
  have half of the short call slots of the add-on in use (at least one). A client
  call takes one of those slots, so the add-on never gets more calls at once than
  it declared, and the tool calls of chats keep the other half. A call past that
  limit gets a 503.
- **Revoking a client** ends its open streams at the next check, stops its
  running jobs and frees its place. The host cannot tell an add-on to forget a
  client. An add-on should remove the outbox of a client that has been silent for
  a long time; the reference add-on removes files untouched for a week, and looks
  for them at most once an hour, when a client asks for its outbox. The job
  records of the client stay until the normal retention removes them. A key that
  expires is not a revoke: the jobs it started end at their own time.
- A stream also ends when the owner account is disabled.
- Add-ons and the client routes run on the instance that owns the add-ons. A
  frontend of a distributed deployment that does not own them answers `503`.
- The add-on updates its outbox while a stream is open or a route runs. A job
  that ends while the client is away shows up when the client reconnects.
- The owner trusts the add-on. A client is held to its own jobs by the host, and
  the add-on tells the host which operation it is serving by the operation id.
