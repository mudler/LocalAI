+++
title = "Remotes"
toc = true
description = "Use the models of another LocalAI, an OpenAI-compatible service or Anthropic as models of this instance"
tags = ["Proxy", "Cloud", "Routing", "Administration"]
categories = ["Features"]
weight = 115
devbuild = "feature"
+++

Use a model that runs on another server as if it ran here. Save the server's
address and key once as a **remote**, then pick the models to add. The server
is another LocalAI, an OpenAI-compatible service or Anthropic.

LocalAI adds each chosen model to your model list as an **entry**. An entry is
a normal model config. You use it like any other model:

- Clients call it by name in the chat APIs.
- It can be a target of a [failover chain]({{% relref "/models/model-failover" %}}).
- API keys, per-user access and usage statistics apply to it.

The key of the other server is stored once, on the remote. The entries
do not hold a key.

For the model config of a single model on another server, see
[Cloud passthrough proxy]({{% relref "/administration/cloud-proxy" %}}). A
remote writes that kind of config for you.

## Kinds of remote

| Kind | Server behind the remote | Backend of the entries | Models you can add | Key |
|---|---|---|---|---|
| `localai` | Another LocalAI | `localai-proxy` | Every model the server lists, with the modalities it reports (see below) | Optional |
| `openai` | An OpenAI-compatible service | `cloud-proxy` | Chat models | Optional |
| `anthropic` | Anthropic, or a gateway for it | `cloud-proxy` | Chat models | Required |

**`localai`.** LocalAI asks the server for `GET /v1/models` to get the model
ids. It then asks `GET /v1/models/capabilities` to learn what each model does,
and writes that as `known_usecases` in the entry. If the server does not
answer the second call, LocalAI sets `chat` for every model. LocalAI keeps only
the usecases that it knows. It drops modifiers such as `tools` and
`thinking`. It also drops `decisions`, `score` and `token_classify`, because
these reserve a model for a local primitive. If no usecase is left, LocalAI
sets `chat`.

{{% callout style="warning" %}}
A model that the other server reports only as `score` becomes a chat entry here.
{{% /callout %}}

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
`https://api.anthropic.com`. The form in the web interface has no address
field for this kind, so it always uses Anthropic. To use a gateway for
Anthropic, send its address in the `url` field of the
[API](#api-reference).

The entries of the cloud kinds use `mode: passthrough` and declare the
`chat` usecase only. They do not accept images, whatever the model on the server
can do. To change this for one entry, edit the entry in the model editor.

For the OpenAI-compatible kind, the web interface offers the providers
**OpenAI**, **OpenRouter**, **Groq** and **Custom**. A provider fills in the
name and the address. You still enter the key. The web interface asks for a
key for this kind. The API accepts a remote without one.

## Create a remote

Only administrators see the controls below.

### From the first run

1. On the first-run screen, at **Where do your models run?**, choose
   **On another server or service**.
2. Continue with the steps below. The first run shows them as two steps:
   **Connect a remote** and **Choose models**.

### From the Models page

1. Open **Models**.
2. In the page header, select **Connect a remote**.
3. Continue with the steps below.

### Steps

1. At **Type of remote**, choose **LocalAI**, **OpenAI-compatible** or
   **Anthropic**.
2. For **OpenAI-compatible**, choose a **Provider**. For **LocalAI** and a
   **Custom** provider, enter the **Address**. **Anthropic** has no address
   field.
3. Enter the **API key**. The key is optional for **LocalAI** only.
4. Select **Test connection**. LocalAI contacts the server and lists its
   models. If the test fails, the message names the step that failed (see
   [Troubleshooting](#troubleshooting)).
5. Choose the models to add. For a LocalAI server, all models start ticked.
   For the cloud kinds, no model is ticked. You must tick at least one model.
6. Change the **Name** of the remote if you want to.
7. Select **Connect and add models**.

LocalAI saves the remote and creates one entry for each chosen model.
Then it refreshes the remote once. If the server fails at that moment,
LocalAI still keeps the remote and records the error in its status.

To see your remotes, open **Models** and select the **Remotes** tab.
Each remote has a card with its status, the number of its models,
**Refresh** and **Edit**. **View in Installed** opens the **Installed** tab
with the **Remote** filter, which lists the entries that come from
remotes. An entry row has the **Remote model** mark, and the size column
shows **Remote**. The row has no **Load** or **Stop** button.

### Entry names

LocalAI names an entry after the model id on the other server.

1. LocalAI replaces every character that is not a letter, a digit, `.`, `_`
   or `-` with `-`. It collapses repeated `-` and trims `-` and `.` from both
   ends. The name has at most 120 characters.
2. If that name is free, the entry gets it. A name is free when no model
   config and no entry of any remote uses it.
3. Otherwise the entry is named `<remote-name>-<id>`. The remote name
   is lowercased and sanitized in the same way.
4. If that name is taken too, LocalAI skips the model and reports the reason.

For example, the model `meta/llama-3` on a remote named `Lab` becomes the
entry `meta-llama-3`. If that name is taken, it becomes `lab-meta-llama-3`.

The entry sets `proxy.upstream_model` to the exact id on the other server, so
the name of the entry does not change what that server receives.

## How LocalAI keeps the key

- You enter the key once, on the remote. The key must be printable ASCII,
  with no spaces or line breaks.
- LocalAI seals the key in the store of the remotes with AES-256-GCM. The
  seal is tied to the remote, so a sealed value copied to another record does
  not open.
- The API never returns the key. A remote shows `key_set: true` or
  `false` only.
- LocalAI does not write the key to a log, to an error message, to an
  entry file or to the node registry of a cluster.
- An entry holds `proxy.remote: <id>` and no credential.
- LocalAI reads the key when it sends a load of the model, not on every
  request. The key goes into the load options at that moment and nowhere else.
- The key is bound to the address of the remote. LocalAI gives the key to a
  model only when the `proxy.upstream_url` of the model has the same scheme,
  host and port as the remote, and its path is the path of the remote or a
  path below it. The entries that LocalAI creates always match. If you point
  an entry at another server, the entry fails to load with the error
  "proxy.upstream_url is not an address of this remote". To use that entry
  with another server, remove `proxy.remote` from it and give it its own key.
- When you change the key of a remote, LocalAI stops every loaded model that
  names the remote in `proxy.remote`. The next request loads the model again
  with the new key.

To change the key in the web interface, select **Edit** on the card of the
remote and enter the key in **New API key**. If you leave the field empty,
the key that is set stays. The web interface cannot remove a key. To remove
it, send an empty `api_key` to the [API](#change). The **Edit** sheet has no
address field for an Anthropic remote.

LocalAI stores the sealing key next to the records. The seal protects a copy of
one record. It does not protect against a person who can read the whole store.
Keep the data directory and the database private.

If the sealing key is lost while sealed keys exist, LocalAI does not make a new
one, because the stored keys would never open again. The log shows an error and
the remote routes that need the key fail. Restore the key file
(`remotes.key`) or the database row. In standalone mode LocalAI sets the mode
of the `remotes` folder to 0700 and of `remotes.json` to 0600 if they are
looser. It refuses a `remotes.key` that others can read.

`proxy.remote` cannot be combined with `api_key`, `api_key_env` or
`api_key_file`. LocalAI refuses such a config.

## Keep the model list in sync

LocalAI compares the list of the server with your entries when you select
**Refresh** on a remote, and once at startup. LocalAI does not poll.

- A model that the server lists and that has no entry goes to the **New** list
  of the remote. LocalAI does not add it. You add it when you choose: the
  card of the remote shows the new models, and you tick them and select
  **Add selected**. **Not now** hides the list until a new model arrives or
  you reload the page.
- A model that an entry uses, and that the server no longer lists, stays as an
  entry. The card of the remote counts it as no longer listed, and in
  **Installed** the state of the entry is **Unavailable upstream**. LocalAI
  deletes nothing.
- A refresh never changes an entry file. You can edit an entry in the model
  editor, and a refresh keeps your edit.
- A refresh records the status of the remote: `ok`, `auth_failed` or
  `unreachable` (`never` before the first refresh). It also records the time
  and the last error.
- A server that lists no usable model is reachable. The status is `ok`, the
  last error says so, and every entry shows as unavailable.
- If you deleted an entry file outside the remote, the next refresh drops
  that entry from the list of the remote.

LocalAI does change an entry in two cases, both after you edit the remote:

- If you change the address, LocalAI updates `proxy.upstream_url` on the
  entries that still hold the old value. An entry that you set to another
  value is not changed. That entry then fails to load, because the key is
  bound to the new address.
- If you change the key, LocalAI stops the loaded models, as described above.

When you change the key or the address, LocalAI also refreshes the remote.
The status, the last error and the **New** list then belong to the new key
or address. A change of the name only does not contact the server.

## Remove a remote

To remove a remote, open the **Remotes** tab, select **Edit** on its
card, and then select **Remove remote**. A dialog asks what happens to the
entries:

- **Remove models too.** LocalAI deletes the entry configs and stops the
  models.
- **Keep the models.** The entries stay as plain models. LocalAI clears
  `proxy.remote` in each one (the file keeps `remote: ""`) and stops it.
  LocalAI does not copy the key into the entry. If the server needs a key, set one in the model editor
  (`api_key`, `api_key_env` or `api_key_file`) before you use the entry.

**Keep the models** is the default, in the dialog and in the API. An entry
that you detached by hand earlier is not touched.
If LocalAI cannot handle some entries, the remote stays so that you can
try again. If another frontend adds a model to the remote while you remove it,
the remote also stays. Remove it again.

## Network and security

- Only administrators can use the remote routes.
- The server that receives the client request contacts the other server. In
  [distributed mode](#distributed-mode), the worker that runs the proxy backend
  sends the model requests.
- Private network addresses are allowed on purpose. You can connect to a
  server on your own network.
- When LocalAI tests or refreshes a remote, it refuses cloud metadata
  addresses. These are `169.254.169.254`, `169.254.170.2`, `169.254.170.23`,
  the rest of `169.254.169.0/24` and `169.254.170.0/24`, `fd00:ec2::254`,
  `fd00:ec2::23`, `fd20:ce::254`, `168.63.129.16`, `192.0.0.192` and
  `100.100.100.200`. The test reports `invalid_url` for them.
- LocalAI refuses redirects when it tests or refreshes a remote. Use the final
  address of the server.
- When LocalAI tests or refreshes a remote, each call to the other server has
  a limit of 15 seconds and 4 MiB of response data. The limits apply to each
  call. A `localai` listing makes two calls. An `anthropic` listing can make up
  to 20 calls, so it can take up to 5 minutes and read up to 80 MiB. These
  calls do not use the proxy settings of the environment (`HTTPS_PROXY`).
- The rules above apply to the test and the refresh only. The model requests
  go through the proxy backends, which do not refuse metadata addresses and
  which use the proxy settings of the environment. So model requests, and the
  key, can go through a proxy that `HTTPS_PROXY` names on the host that runs
  the backend.
- An address must start with `http://` or `https://` and must not hold a user
  name, a password, a query or a fragment. Use the key field for the key.
- The entries of the `openai` and `anthropic` kinds use `cloud-proxy`. The PII
  filter is on by default for them. The entries of the `localai` kind use
  `localai-proxy`, and the PII filter is off by default for them. Set
  `pii.enabled` in the model editor to change either default. See
  [Middleware]({{% relref "/administration/middleware" %}}) and
  [PII filtering]({{% relref "/models/model-configuration#pii-filtering" %}}).

## Distributed mode

Remotes follow the rules of [distributed mode]({{% relref "/administration/distributed-mode" %}}).

- **The record is shared state.** In a cluster, the remote records are in
  the cluster database (PostgreSQL). LocalAI keeps no copy in memory. Every
  call reads the database, so every frontend shows the same list and the same
  status.
- **One frontend runs the startup refresh.** The frontends take an advisory
  lock. The frontend that gets it refreshes every remote. The others skip
  the pass. The result is in the shared record. You can select **Refresh** on
  any frontend at any time.
- **Entries are normal model configs.** LocalAI writes them through the same
  path as the model editor and tells the other frontends that the model
  changed, as it does for any model edit.
- **The key is resolved when a load is sent.** When a frontend sends a load of
  an entry to a worker, it reads the key from the shared store and puts it in
  the load options. A request for a model that is already loaded reads no key.
  You do not need a key file or an environment variable on the workers.
- **The node registry does not store the key.** The registry keeps the load
  options of a model so that the reconciler can start more replicas. LocalAI
  removes the key from these options before it stores them. When the reconciler
  starts a replica, LocalAI reads the key again, with the address check. A
  removed remote makes that load fail with `key unavailable`, and LocalAI sends
  nothing to the worker.
- **The key crosses the cluster network.** The frontend sends the load options
  to the worker over the backend gRPC connection, which is not encrypted. This
  is the same for every backend secret in the load options. If that is not
  acceptable, write the entry by hand without `proxy.remote` and use
  `api_key_file` or `api_key_env` with a file or a variable on the workers.
- **The worker sends the requests.** The worker that runs the proxy backend
  contacts the other server. Every worker that can serve these models must be
  able to reach its address on the network. The test of the remote runs on the
  frontend only. A passed test does not prove that a worker can reach the
  server.
- **A key change reaches the workers on the next load.** When you change the
  key, LocalAI stops every loaded model that names the remote, including the
  replicas on workers. The next load, and the next replica that the reconciler
  starts, reads the new key.

In standalone mode there is no database. LocalAI keeps the records in the
files `remotes.json` and `remotes.key` in the `remotes` folder of
the data path (`--data-path`). Both files have mode 0600. If you set
`--data-path=-`, LocalAI keeps the remotes in memory and loses them at
restart. The entry files stay, but their key cannot be resolved without the
remote.

If the store cannot open at startup, the log says why. The remote routes
return `503`, and an entry that needs a remote fails to load with the error
`key unavailable`. The rest of the server runs as usual.

## API reference

All routes need an administrator. Bodies and answers are JSON. The key is
write-only: you can send it, and no route returns it.

The routes do not depend on the model gallery. They work when you start LocalAI
with `--disable-gallery-endpoint`.

| Method and path | Purpose | Success |
|---|---|---|
| `POST /api/remotes/test` | List the models of a server. Saves nothing. | `200` |
| `POST /api/remotes` | Create a remote and its first entries. | `201` |
| `GET /api/remotes` | List the remotes. | `200` |
| `GET /api/remotes/:id` | Get one remote. | `200` |
| `PATCH /api/remotes/:id` | Change the name, the address or the key. | `200` |
| `DELETE /api/remotes/:id?entries=remove\|keep` | Remove the remote. The default is `keep`. | `204` |
| `POST /api/remotes/:id/refresh` | Ask the server for its models and record the answer. | `200` |
| `POST /api/remotes/:id/adopt` | Create entries for more models. | `200` |

Errors:

| Status | When |
|---|---|
| `400` | The request is not valid. This includes a body that is not one JSON object, a field that the route does not know, and an `entries` value other than `keep` or `remove`. |
| `404` | The remote does not exist. |
| `409` | The name is taken. Names are compared without case. |
| `413` | The body is larger than 1 MiB. |
| `500` | An internal error. If a change or a removal was only partly done, the message says so. Read the remote again and check the server log. A create or an adopt that was only partly done is not a `500`: see [Create](#create). |
| `503` | The store of the remotes did not open at startup. |

An error body has the shape
`{"error": {"message": "...", "code": 404, "type": "remote_error"}}`.

### Test

```bash
curl -s https://localai.example.com/api/remotes/test \
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
curl -s https://localai.example.com/api/remotes \
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
`anthropic` kind needs a key. You can send at most 500 models in one call.
If you send no models, LocalAI creates no entry. The models that the server
lists are only recorded in the **New** list of the remote, and you add them
later with the adopt call.

The answer is `201`:

```json
{
  "remote": {
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

A skipped model has an `id` and a `reason`. The reasons are:

- `listed twice`
- `already adopted`
- `no free model name`
- `a model with that name exists`
- `the model name or settings are not valid`
- `could not build the entry`
- `could not write the entry`

If LocalAI saved the remote but could not add some models, the answer is still
`201`. It has a `warning` field with a message, and `created` and `skipped`
say what was done. Read the remote, then add the missing models with the
adopt call. Do not send the create again: the name is taken and the answer
would be `409`. Without a problem, the answer has no `warning` field.

### List, get, refresh

```bash
curl -s https://localai.example.com/api/remotes \
  -H "Authorization: Bearer <admin-key>"

curl -s -X POST https://localai.example.com/api/remotes/<id>/refresh \
  -H "Authorization: Bearer <admin-key>"
```

The list answer is `{"data": [<remote>, ...]}`. A refresh returns the
remote. A server that fails does not make the refresh fail. The failure is
in the `status` and `last_error` fields.

### Change

```bash
curl -s -X PATCH https://localai.example.com/api/remotes/<id> \
  -H "Authorization: Bearer <admin-key>" \
  -H "Content-Type: application/json" \
  -d '{"api_key": "<new-key>"}'
```

You can send `name`, `url` and `api_key`. A field that you leave out does not
change. An empty `api_key` removes the key. The `anthropic` kind refuses an
empty key. When `url` or `api_key` changes, LocalAI refreshes the remote before
it answers, so `status`, `last_error`, `new` and `unavailable` in the answer
are current. A change of `name` only does not refresh.

### Add models

```bash
curl -s https://localai.example.com/api/remotes/<id>/adopt \
  -H "Authorization: Bearer <admin-key>" \
  -H "Content-Type: application/json" \
  -d '{"models": [{"id": "qwen3-8b", "usecases": ["chat"]}]}'
```

The answer has the same shape as the answer of create. You must send at least
one model and at most 500. If some models were added but the rest failed, the
answer is still `200` with a `warning` field.

### Remove

```bash
curl -s -X DELETE "https://localai.example.com/api/remotes/<id>?entries=remove" \
  -H "Authorization: Bearer <admin-key>"
```

The answer is `204` with no body.

### Remote models in the model list

`GET /api/models/capabilities` gives three more fields for each model:

| Field | Value |
|---|---|
| `remote` | `true` when a remote backend (`cloud-proxy` or `localai-proxy`) serves the model. It is `false` for a local model. |
| `remote_id` | The id of the remote that owns the entry. It is not there for a model without a remote, and an administrator only gets it. |
| `upstream_state` | `ok`, or `unavailable` when the server no longer lists the model. It is not there for a local model. |

## Troubleshooting

When you test a remote, LocalAI checks in this order and reports the first
step that fails:

1. `invalid_url`: the address is not valid, or it is a blocked address.
2. `unreachable`: LocalAI cannot get an answer.
3. `auth`: the server answered `401` or `403`.
4. `no_models`: the server answered, but lists no usable model.

| Symptom | Cause and action |
|---|---|
| `auth`, "refused the key" | The server rejected the key. Check the key and its permissions. A LocalAI server with API keys needs a key that can list models. |
| `unreachable`, "Could not find" | The host name does not resolve from the LocalAI server. Check the name and the DNS. |
| `unreachable`, "did not answer in time" | The server did not answer in 15 seconds. Check the network and the firewall. |
| `unreachable`, "redirected" | The server redirected the request. Enter the address that it redirects to. |
| `unreachable`, "status 404" or another status | The address is wrong. For `openai`, use the API base, for example `https://api.example.com/v1`. For `localai`, use the root of the server. |
| `invalid_url` | The address needs `http://` or `https://`, a host, and no user name, query or fragment. A cloud metadata address is always refused. |
| `no_models` | The server lists no model, or for `openai` every id looked like a non-chat model. Add the model later with the **adopt** call if you know its id. |
| A model shows **Unavailable upstream** | The server no longer lists the model. The entry stays. Check that the server still has the model, or remove the entry. |
| `409`, name taken | Another remote uses that name. Names are compared without case. |
| A model is skipped, "no free model name" | Both the plain name and the prefixed name are in use. Rename the other model, or rename the remote and add the model again. |
| A kept entry fails with an authentication error | When you keep entries, they lose the key. Set `api_key`, `api_key_env` or `api_key_file` in the model editor. |
| An entry fails to load with "key unavailable" | The remote is gone or its store did not open. Check the startup log. Create the remote again, or detach the entry and give it its own key. |
| An entry fails to load with "not an address of this remote" | The `proxy.upstream_url` of the entry is not below the address of the remote, so LocalAI does not send the key. Set the address back, or remove `proxy.remote` and give the entry its own key. |
| The test says "Only the first 2000 are shown" | The server lists more than 2,000 usable models. LocalAI keeps the first 2,000 by id. Add other models with the adopt call if you know their ids. |
| A model works in a test but fails in a cluster | A worker cannot reach the address of the server. Test from the worker host. |

## Limits

- The cloud kinds (`openai` and `anthropic`) create chat entries only.
- A listing keeps at most 2,000 models, the first by id. A test then says so
  in its `message`, and a refresh writes a warning to the log. The status
  stays `ok` and `last_error` stays empty.
- One create or adopt call adds at most 500 models.
- LocalAI does not poll the server. New models appear after a refresh or a
  restart. The list of **New** models can be out of date until then.
- The modalities of a LocalAI server depend on the answer to
  `GET /v1/models/capabilities`. An older server, or a key that cannot use that
  route, gives `chat` for every model. Change `known_usecases` in the model
  editor if the entry needs more.
- The `openai` listing is filtered by model name. The filter is a guess.
- The limits of the proxy backends apply to the entries. See
  [Cloud passthrough proxy]({{% relref "/administration/cloud-proxy" %}}).
