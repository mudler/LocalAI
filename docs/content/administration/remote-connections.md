+++
title = "Remote connections"
toc = true
description = "Use the models of another LocalAI, an OpenAI-compatible service or Anthropic as models of this instance"
tags = ["Proxy", "Cloud", "Routing", "Administration"]
categories = ["Features"]
weight = 115
+++

A **connection** is a saved address and key of a remote server. The remote
server is another LocalAI, an OpenAI-compatible service or Anthropic. You
choose which remote models to use. LocalAI creates one model **entry** for
each chosen model.

An entry is a normal model config. You use it like any other model:

- Clients call it by name in the chat APIs.
- It can be a target of a [failover chain]({{% relref "/models/model-failover" %}}).
- API keys, per-user access and usage statistics apply to it.

The key of the remote server is stored once, on the connection. The entries
do not hold a key.

For the model config of a single remote model, see
[Cloud passthrough proxy]({{% relref "/administration/cloud-proxy" %}}). A
connection writes that kind of config for you.

## Kinds of connection

| Kind | Remote server | Backend of the entries | Models you can add | Key |
|---|---|---|---|---|
| `localai` | Another LocalAI | `localai-proxy` | Every model the remote lists, for every modality it reports | Optional |
| `openai` | An OpenAI-compatible service | `cloud-proxy` | Chat models | Optional |
| `anthropic` | Anthropic, or a gateway for it | `cloud-proxy` | Chat models | Required |

**`localai`.** LocalAI asks the remote for `GET /v1/models` to get the model
ids. It then asks `GET /v1/models/capabilities` to learn what each model does,
and writes that as `known_usecases` in the entry. If the remote does not
answer the second call, LocalAI sets `chat` for every model. LocalAI keeps only
usecases that `known_usecases` accepts. If none is left, it sets `chat`.

**`openai`.** LocalAI asks `GET <address>/models`. The OpenAI listing does not
say what a model does, so LocalAI judges by name. It leaves out ids that
contain `embed`, `whisper`, `transcribe`, `tts`, `dall-e`, `image`,
`moderation`, `audio` or `realtime`. If LocalAI leaves out a chat model by
mistake, add it later with the **adopt** call (see the
[API reference](#api-reference)). The address is the API base of the service,
for example `https://api.example.com/v1`. LocalAI removes a trailing
`/chat/completions` or `/models`.

**`anthropic`.** LocalAI asks `GET <address>/v1/models`, up to 20 pages of 100
models. The address is optional. If you leave it empty, LocalAI uses
`https://api.anthropic.com`. Use the address field for a gateway.

The entries of the cloud kinds use `mode: passthrough` and declare the
`chat` usecase only. They do not accept images, whatever the remote model can
do. To change this for one entry, edit the entry in the model editor.

The interface offers presets for common OpenAI-compatible services. A preset
fills in the name and the address. You still enter the key.

## Create a connection

### From the first run

1. On the first-run screen, at **Where do your models run?**, choose
   **On another server or service**.
2. Continue with the steps below.

### From the Models page

1. Open **Models**.
2. Select **Connect a remote**.
3. Continue with the steps below.

### Steps

1. Choose the kind of connection.
2. Enter the address. For `anthropic`, leave it empty to use Anthropic.
3. Enter the key, if the remote needs one.
4. Select **Test connection**. LocalAI contacts the remote and lists its
   models. If the test fails, the message names the step that failed (see
   [Troubleshooting](#troubleshooting)).
5. Choose the models to add. For a remote LocalAI, all models start ticked.
   For the cloud kinds, no model is ticked.
6. Select **Connect and add models**.

LocalAI saves the connection and creates one entry for each chosen model.
Then it refreshes the connection once. If the remote fails at that moment,
LocalAI still keeps the connection and records the error in its status.

To see your connections, open **Models** and select the **Connections** tab.
In **Installed**, use the **Remote** filter to list the entries that come from
connections.

### Entry names

LocalAI names an entry after the model id on the remote server.

1. LocalAI replaces every character that is not a letter, a digit, `.`, `_`
   or `-` with `-`. It collapses repeated `-` and trims `-` and `.` from both
   ends. The name has at most 120 characters.
2. If that name is free, the entry gets it. A name is free when no model
   config and no entry of any connection uses it.
3. Otherwise the entry is named `<connection-name>-<id>`. The connection name
   is lowercased and sanitized in the same way.
4. If that name is taken too, LocalAI skips the model and reports the reason.

For example, the model `meta/llama-3` on a connection named `Lab` becomes the
entry `meta-llama-3`. If that name is taken, it becomes `lab-meta-llama-3`.

The entry sets `proxy.upstream_model` to the exact id on the remote server, so
the name of the entry does not change what the remote receives.

## How LocalAI keeps the key

- You enter the key once, on the connection.
- LocalAI seals the key at rest with AES-256-GCM. The seal is tied to the
  connection, so a sealed value copied to another record does not open.
- The API never returns the key. A connection shows `key_set: true` or
  `false` only.
- LocalAI does not write the key to a log, to an error message or to an
  entry file.
- An entry holds `proxy.connection: <id>` and no credential.
- LocalAI reads the key when it loads the model, not on every request. The
  key goes into the load options at that moment.
- When you change the key of a connection, LocalAI stops the loaded entries
  of that connection. The next request loads the entry again with the new key.

LocalAI stores the sealing key next to the records. The seal protects a copy of
one record. It does not protect against a person who can read the whole store.
Keep the data directory and the database private.

`proxy.connection` cannot be combined with `api_key`, `api_key_env` or
`api_key_file`. LocalAI refuses such a config.

## Keep the model list in sync

LocalAI compares the remote list with your entries when you select
**Refresh** on a connection, and once at startup. LocalAI does not poll.

- A model that the remote lists and that has no entry goes to the **New** list
  of the connection. LocalAI does not add it. You add it when you choose.
- A model that an entry uses, and that the remote no longer lists, stays as an
  entry. The connection shows it as **Unavailable upstream**. LocalAI deletes
  nothing.
- A refresh never changes an entry file. You can edit an entry in the model
  editor, and a refresh keeps your edit.
- A refresh records the status of the connection: `ok`, `auth_failed` or
  `unreachable` (`never` before the first refresh). It also records the time
  and the last error.
- A remote that lists no usable model is reachable. The status is `ok`, the
  last error says so, and every entry shows as unavailable.
- If you deleted an entry file outside the connection, the next refresh drops
  that entry from the list of the connection.

LocalAI does change an entry in two cases, both after you edit the connection:

- If you change the address, LocalAI updates `proxy.upstream_url` on the
  entries that still hold the old value. An entry that you set to another
  value is not changed.
- If you change the key, LocalAI stops the loaded entries, as described above.

## Remove a connection

When you remove a connection, you choose what happens to its entries:

- **Remove the entries.** LocalAI deletes the entry configs and stops the
  models.
- **Keep the entries.** The entries stay as plain models. LocalAI removes
  `proxy.connection` from each one and stops it. LocalAI does not copy the key
  into the entry. If the remote needs a key, set one in the model editor
  (`api_key`, `api_key_env` or `api_key_file`) before you use the entry.

Keep is the default. An entry that you detached by hand earlier is not touched.
If LocalAI cannot handle some entries, the connection stays so that you can
try again.

## Network and security

- Only administrators can use the connection routes.
- The server that receives the client request contacts the remote. In
  [distributed mode](#distributed-mode), the worker that runs the proxy backend
  sends the model requests.
- Private network addresses are allowed on purpose. You can connect to a
  server on your own network.
- Cloud metadata addresses are refused. These include `169.254.169.254`,
  `169.254.170.2`, `fd00:ec2::254`, `168.63.129.16`, `192.0.0.192` and
  `100.100.100.200`. The test reports `invalid_url` for them.
- LocalAI refuses redirects. Use the final address of the remote.
- Each call to the remote has a limit of 15 seconds and 4 MiB of response
  data. The system proxy settings are not used.
- An address must start with `http://` or `https://` and must not hold a user
  name, a password, a query or a fragment. Use the key field for the key.
- The entries of the `openai` and `anthropic` kinds use `cloud-proxy`. The PII
  filter is on by default for them. The entries of the `localai` kind use
  `localai-proxy`, and the PII filter is off by default for them. Set
  `pii.enabled` in the model editor to change either default. See
  [Middleware]({{% relref "/administration/middleware" %}}) and
  [PII filtering]({{% relref "/models/model-configuration#pii-filtering" %}}).

## Distributed mode

Remote connections follow the rules of [distributed mode]({{% relref "/administration/distributed-mode" %}}).

- **The record is shared state.** In a cluster, the connection records are in
  the cluster database (PostgreSQL). LocalAI keeps no copy in memory. Every
  call reads the database, so every frontend shows the same list and the same
  status.
- **One frontend runs the startup refresh.** The frontends take an advisory
  lock. The frontend that gets it refreshes every connection. The others skip
  the pass. The result is in the shared record. You can select **Refresh** on
  any frontend at any time.
- **Entries are normal model configs.** LocalAI writes them through the same
  path as the model editor and tells the other frontends that the model
  changed, as it does for any model edit.
- **The key is resolved on the frontend.** When a model loads, the frontend
  reads the key and sends it to the worker in the load options. You do not
  need a key file or an environment variable on the workers.
- **The worker sends the requests.** The worker that runs the proxy backend
  contacts the remote. Every worker that can serve these models must be able to
  reach the remote address on the network. The test of the connection runs on
  the frontend only. A passed test does not prove that a worker can reach the
  remote.
- **A key change reaches the workers.** When you change the key, LocalAI
  stops the loaded entries, including the replicas on workers. The next load
  uses the new key.

In standalone mode there is no database. LocalAI keeps the records in the
files `connections.json` and `connections.key` in the `connections` folder of
the data path (`--data-path`). Both files have mode 0600. If you set
`--data-path=-`, LocalAI keeps the connections in memory and loses them at
restart. The entry files stay, but their key cannot be resolved without the
connection.

If the store cannot open at startup, the log says why. The connection routes
return `503`, and an entry that needs a connection fails to load with the error
`key unavailable`. The rest of the server runs as usual.

## API reference

All routes need an administrator. Bodies and answers are JSON. The key is
write-only: you can send it, and no route returns it.

| Method and path | Purpose | Success |
|---|---|---|
| `POST /api/connections/test` | List the models of a remote. Saves nothing. | `200` |
| `POST /api/connections` | Create a connection and its first entries. | `201` |
| `GET /api/connections` | List the connections. | `200` |
| `GET /api/connections/:id` | Get one connection. | `200` |
| `PATCH /api/connections/:id` | Change the name, the address or the key. | `200` |
| `DELETE /api/connections/:id?entries=remove\|keep` | Remove the connection. The default is `keep`. | `204` |
| `POST /api/connections/:id/refresh` | Ask the remote for its models and record the answer. | `200` |
| `POST /api/connections/:id/adopt` | Create entries for more models. | `200` |

Errors: `400` for an invalid request, `404` for an unknown connection, `409`
when the name is taken (names are compared without case), `503` when the store
cannot open.

### Test

```bash
curl -s https://localai.example.com/api/connections/test \
  -H "Authorization: Bearer <admin-key>" \
  -H "Content-Type: application/json" \
  -d '{"kind":"localai","url":"https://remote.example.com","api_key":"<key>"}'
```

A failed test is still a `200`. The field `ok` is `false`, and `step` says
what failed.

```json
{"ok": true, "models": [{"id": "gemma-3-12b", "usecases": ["chat"]}]}
```

```json
{"ok": false, "step": "auth", "message": "The server refused the key (status 401)."}
```

`step` is one of `invalid_url`, `unreachable`, `auth` and `no_models`.

### Create

```bash
curl -s https://localai.example.com/api/connections \
  -H "Authorization: Bearer <admin-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "kind": "localai",
    "name": "Lab",
    "url": "https://remote.example.com",
    "api_key": "<key>",
    "models": [{"id": "gemma-3-12b", "usecases": ["chat"]}]
  }'
```

`usecases` is only used for the `localai` kind. The cloud kinds are always
chat. The name has at most 80 characters and no control characters. The
`anthropic` kind needs a key. If you send no models, the connection starts with
the models it finds in its **New** list.

The answer is `201`:

```json
{
  "connection": {
    "id": "<id>",
    "name": "Lab",
    "kind": "localai",
    "url": "https://remote.example.com",
    "key_set": true,
    "status": "ok",
    "last_refresh": "2026-11-14T10:00:00Z",
    "entries": ["gemma-3-12b"],
    "unavailable": [],
    "new": [{"id": "qwen3-8b", "usecases": ["chat"]}]
  },
  "created": ["gemma-3-12b"],
  "skipped": []
}
```

A skipped model has an `id` and a `reason`. The reasons are `listed twice`,
`already adopted`, `no free model name` and `a model with that name exists`.

### List, get, refresh

```bash
curl -s https://localai.example.com/api/connections \
  -H "Authorization: Bearer <admin-key>"

curl -s -X POST https://localai.example.com/api/connections/<id>/refresh \
  -H "Authorization: Bearer <admin-key>"
```

The list answer is `{"data": [<connection>, ...]}`. A refresh returns the
connection. A remote that fails does not make the refresh fail. The failure is
in the `status` and `last_error` fields.

### Change

```bash
curl -s -X PATCH https://localai.example.com/api/connections/<id> \
  -H "Authorization: Bearer <admin-key>" \
  -H "Content-Type: application/json" \
  -d '{"api_key": "<new-key>"}'
```

You can send `name`, `url` and `api_key`. A field that you leave out does not
change. An empty `api_key` removes the key. The `anthropic` kind refuses an
empty key.

### Add models

```bash
curl -s https://localai.example.com/api/connections/<id>/adopt \
  -H "Authorization: Bearer <admin-key>" \
  -H "Content-Type: application/json" \
  -d '{"models": [{"id": "qwen3-8b", "usecases": ["chat"]}]}'
```

The answer has the same shape as the answer of create. You must send at least
one model.

### Remove

```bash
curl -s -X DELETE "https://localai.example.com/api/connections/<id>?entries=remove" \
  -H "Authorization: Bearer <admin-key>"
```

## Troubleshooting

When you test a connection, LocalAI checks in this order and reports the first
step that fails:

1. `invalid_url`: the address is not valid, or it is a blocked address.
2. `unreachable`: LocalAI cannot get an answer.
3. `auth`: the remote answered `401` or `403`.
4. `no_models`: the remote answered, but lists no usable model.

| Symptom | Cause and action |
|---|---|
| `auth`, "refused the key" | The remote rejected the key. Check the key and its permissions. A remote LocalAI with API keys needs a key that can list models. |
| `unreachable`, "Could not find" | The host name does not resolve from the LocalAI server. Check the name and the DNS. |
| `unreachable`, "did not answer in time" | The remote did not answer in 15 seconds. Check the network and the firewall. |
| `unreachable`, "redirected" | The remote redirected the request. Enter the address that it redirects to. |
| `unreachable`, "status 404" or another status | The address is wrong. For `openai`, use the API base, for example `https://api.example.com/v1`. For `localai`, use the root of the server. |
| `invalid_url` | The address needs `http://` or `https://`, a host, and no user name, query or fragment. A cloud metadata address is always refused. |
| `no_models` | The remote lists no model, or for `openai` every id looked like a non-chat model. Add the model later with the **adopt** call if you know its id. |
| A model shows **Unavailable upstream** | The remote no longer lists the model. The entry stays. Check that the remote still has the model, or remove the entry. |
| `409`, name taken | Another connection uses that name. Names are compared without case. |
| A model is skipped, "no free model name" | Both the plain name and the prefixed name are in use. Rename the other model, or rename the connection and add the model again. |
| A kept entry fails with an authentication error | When you keep entries, they lose the key. Set `api_key`, `api_key_env` or `api_key_file` in the model editor. |
| An entry fails to load with "key unavailable" | The connection is gone or its store did not open. Check the startup log. Create the connection again, or detach the entry and give it its own key. |
| A model works in a test but fails in a cluster | A worker cannot reach the remote address. Test from the worker host. |

## Limits

- The cloud kinds (`openai` and `anthropic`) create chat entries only.
- LocalAI does not poll the remote. New models appear after a refresh or a
  restart. The list of **New** models can be out of date until then.
- The modalities of a remote LocalAI depend on the answer to
  `GET /v1/models/capabilities`. An older remote, or a key that cannot use that
  route, gives `chat` for every model. Change `known_usecases` in the model
  editor if the entry needs more.
- The `openai` listing is filtered by model name. The filter is a guess.
- The limits of the proxy backends apply to the entries. See
  [Cloud passthrough proxy]({{% relref "/administration/cloud-proxy" %}}).
