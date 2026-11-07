+++
disableToc = false
title = "Knowledge (collections)"
weight = 21
url = "/features/collections/"
+++

Knowledge is the part of LocalAI that holds collections of documents that agents
can search. In the web UI it is the **Knowledge** item in the sidebar. Each item
in it is a collection: a named set of documents that LocalAI splits into chunks and
stores as embeddings. You can search a collection by meaning. A
[saved agent](/features/chats-agents/) can list the collections it may search,
and a [chat](/features/chats/) with that agent gets the tools `list_collections`
and `search_collection`.

Collections are on by default. To turn them off, set `LOCALAI_DISABLE_COLLECTIONS=true`.
The name of this variable is old: it now turns off the collections only.

## How it works

1. You create a collection and upload files to it, or you add a URL as a source.
2. LocalAI splits each document into chunks no larger than `LOCALAI_COLLECTIONS_MAX_CHUNKING_SIZE`.
3. It calls the embedding model to turn each chunk into a vector, and stores the vectors in the vector engine.
4. A search embeds the query and returns the closest chunks.

With the default `chromem` engine a search is semantic only: it finds chunks by
meaning and does not match exact words or codes. The `postgres` engine adds a
keyword search (BM25) and mixes the two results (hybrid search).

The embedding calls go to the LocalAI API of the same server, inside the
process, so they need no API key and work with user accounts. The embedding
model must be installed. The default is `granite-embedding-107m-multilingual`.
When it is not, the embeddings endpoint answers 404 and every upload and
search fails. The Knowledge page then says *No embedding model installed*
and names the model; an administrator gets a button that opens the Models
page with a search for it. `GET /api/collections` reports the same in
`embedding_model` and `embedding_model_installed`.

The Knowledge page also shows how a search finds passages, for example
*Search: semantic*. `GET /api/collections` reports it in `search_mode`: `semantic`
for the `chromem` engine, and `hybrid` for the `postgres` engine.
The same answer gives `max_chunk_size` and `chunk_overlap`, the chunking of a
text in characters.

## Who sees a collection

- With authentication off, there is one set of collections.
- With authentication on, each user has their own collections. A user needs the `collections` feature. Admins always have it.
- A name that is not yours answers `404`. Nobody sees another user's collections, with two exceptions for admins below, and the collections other people share with you.

## Sharing a collection

With accounts, the owner of a collection can share it. Open **Knowledge**, open the
collection and choose **Share**. The [Share dialog]({{%relref "features/sharing" %}})
finds people by name and can share with **Everyone**, every account of the server.
A person can receive it when their account may use collections.

| Role | What the person may do |
|---|---|
| **Can view** (`viewer`) | Search it and read its entries and files. |
| **Can edit** (`editor`) | Also add and remove entries. |

Sources, reset and sharing stay with the owner. A person with either role cannot
list the sources either (`GET /{name}/sources` answers `403`): a source URL can
carry a credential. A shared collection is listed under **Shared with you** with the
owner's name and the role.

The agents and the chats of the person find a shared collection by its name, like
their own. The person's own collection wins a name. When the person has a collection
of the same name, or two people share collections under one name, the shared one is
named `shared:<id>`, where `<id>` is its ID in the sharing API. That name works in
every path below and in the collections of a saved agent. `GET /api/collections`
gives the name to use.

Nothing is copied: a shared collection stays in its owner's store, and an entry that
an editor adds is in the owner's collection. When the owner lowers or removes a
share, LocalAI cancels the person's running searches and writes on it before it
answers, and the next call answers `404`, as for a collection that does not exist. A
download of an entry file (`GET /{name}/entries-raw/{entry}`) runs under the share
too: a lowering or a revocation stops it, and answers only once no more bytes of the
file are sent. A call that the role does not allow answers `403`.

## API

All paths are under `/api/collections`. While the service starts, every
path answers `503` with `collections are starting, please retry shortly`.
When collections are off, the paths do not exist.

| Method and path | Body | Result |
|---|---|---|
| `GET /` | | `{"collections": [...], "count": n, "shared": [...], "shares": {...}, "sharing": true}`. `shared` are the collections shared with you, each with `name` (the name to use), `title` (its own name), `owner` and `role`. `shares` maps each of your collections that you made shareable to `resource_id` and `shared_with`. `sharing` is `false` when this server cannot share. |
| `POST /` | `{"name": "docs"}` | `201` with `{"status": "ok", "name": "docs"}` |
| `POST /{name}/upload` | multipart form, field `file` | `{"status": "ok", "filename": "...", "key": "..."}` |
| `GET /{name}/entries` | | `{"entries": [...], "count": n}` |
| `GET /{name}/entries/{entry}` | | `{"content": "...", "chunk_count": n}` |
| `GET /{name}/entries-raw/{entry}` | | The file as you uploaded it |
| `POST /{name}/search` | `{"query": "...", "max_results": 5}` | `{"results": [...], "count": n}` |
| `DELETE /{name}/entry/delete` | `{"entry": "file.pdf"}` | `{"remaining_entries": [...], "count": n}` |
| `POST /{name}/reset` | | Deletes every entry and keeps the collection and its sources. `{"status": "ok"}` |
| `GET /{name}/sources` | | `{"sources": [...], "count": n}` |
| `POST /{name}/sources` | `{"url": "https://...", "update_interval": 60}` | Follows the URL. `{"status": "ok"}` |
| `DELETE /{name}/sources` | `{"url": "https://..."}` | Stops following the URL |
| `POST /{name}/share` | | Only the owner. `{"resource_id": "...", "shared_with": n}`, the ID of the collection in `/api/shares/collection`. `409` with type `sharing_disabled` when this server cannot share. |

Example:

```bash
curl -X POST http://localhost:8080/api/collections \
  -H "Content-Type: application/json" -d '{"name": "docs"}'
curl -X POST http://localhost:8080/api/collections/docs/upload -F file=@manual.pdf
curl -X POST http://localhost:8080/api/collections/docs/search \
  -H "Content-Type: application/json" -d '{"query": "how do I reset the device?", "max_results": 3}'
```

Notes:

- `update_interval` is in minutes. A value below 1 means 60.
- Names and entries with special characters, such as `:`, must be URL-encoded in the path.
- A `404` means the collection or the entry does not exist for you.
- A search for more passages than the collection holds returns all of them. An empty collection returns none.
- `/readyz` answers `200` only after the collections are ready, so `/api/collections` does not answer `503` once the server is in rotation.
- An upload can fail with `409` when the embedding model changed and the vectors have a different size. Restart LocalAI to embed the collection again, or reset the collection.

### Admin options

With authentication on, an admin can add these query parameters:

- `?all_users=true` on `GET /` also returns the collections of every other user, in `user_groups`, keyed by user ID.
- `?user_id=<id>` on every path except `GET /` and `POST /` works on the collections of that user. Only an administrator may use it.

Other users cannot use either parameter. `user_id` is ignored for them and they always work on their own collections.

## Settings

Set these as environment variables or flags. Most can also be set in the
[runtime settings](/features/runtime-settings/) file, and a change needs a restart.

| Variable | Default | Meaning |
|---|---|---|
| `LOCALAI_DISABLE_COLLECTIONS` | `false` | Turn the collections off. |
| `LOCALAI_COLLECTIONS_STATE_DIR` | The data path (`LOCALAI_DATA_PATH`), then the configuration folder | Where the collections keep their files. With authentication on, each user has a folder `users/<id>` inside it. |
| `LOCALAI_COLLECTIONS_DB_PATH` | `collections` inside the state directory | Where the vector engine keeps its data. |
| `LOCALAI_COLLECTIONS_VECTOR_ENGINE` | `chromem` | The vector engine: `chromem` (files on disk) or `postgres`. |
| `LOCALAI_COLLECTIONS_DATABASE_URL` | empty | The PostgreSQL URL. Used with the `postgres` engine. |
| `LOCALAI_COLLECTIONS_EMBEDDING_MODEL` | `granite-embedding-107m-multilingual` | The model that makes the embeddings. |
| `LOCALAI_COLLECTIONS_MAX_CHUNKING_SIZE` | `400` | The largest size of a chunk. |
| `LOCALAI_COLLECTIONS_CHUNK_OVERLAP` | `0` | How much neighbouring chunks overlap. |
| `LOCALAI_COLLECTIONS_API_URL` | The LocalAI API of this server | The API that the embedding calls go to. Set it only with a key: the calls then leave the process. |
| `LOCALAI_COLLECTIONS_API_KEY` | The first API key of LocalAI | The key for those calls. Without it the embedding calls run inside the process and need none. |
| `LOCALAI_COLLECTIONS_DEFAULT_MODEL` | empty | The default model of the collections service. |

## PostgreSQL vector engine

The default engine keeps the vectors in files on the local disk. To keep them in
PostgreSQL, use a PostgreSQL database and set:

```bash
LOCALAI_COLLECTIONS_VECTOR_ENGINE=postgres
LOCALAI_COLLECTIONS_DATABASE_URL=postgresql://user:password@host:5432/localrecall?sslmode=disable
```

The `docker-compose.yaml` of the project has a commented example with a
PostgreSQL service. With several frontends in
[distributed mode](/features/distributed-mode/), use the `postgres` engine, so
that all frontends share the same collections.

The shares of the collections are records in the policy store of the add-on owner,
keyed by the owner and the name of the collection. A search or a write on a shared
collection runs as admitted work of that policy, which only the owner process holds.
With several frontends, a frontend asks the owner over the add-on carrier: it lists
the shared collections, makes a collection shareable and has each search or write
admitted by the owner, then runs it itself. A revocation on any frontend stops that
work before it answers. When a frontend cannot reach the owner, it stops the work
on shared collections, lists none under `shared` and answers as for a collection
that does not exist; a person's own collections are not affected.

## Use a collection in an agent

Add the names of your collections to the `collections` list of a
[saved agent](/features/chats-agents/). A chat with that agent can then search
only those collections. If a listed collection is deleted, the chat still starts
and the model is told that it is not available.

## Let an agent propose a text for a collection

A model can ask to add a text to a collection, for example a procedure that you
pasted in the chat, with the `propose_change` tool and the kind `knowledge`. It
works like any other [suggested change]({{%relref "features/chats-agents#builtin-grants" %}}),
with these rules:

- **A grant.** The agent needs the grant `knowledge.write` (**Add to knowledge**
  in the Tools list, under **Knowledge**). It is sensitive and off by default. A
  plain chat can propose to any of your collections; a saved agent only to the
  collections on its own list.
- **Always your decision.** The text is never added by the model alone, and never
  at once, whatever your memory save level is: **Ask**, **Auto** and **Always
  confirm** all wait for your **Confirm**. The level **Never** takes the kind
  away. A wrong text would feed the answers of every chat that searches the
  collection, and indexing costs compute, so there is no "save and tell me" for
  Knowledge.
- **What you see.** The card in **Suggested changes** names the collection and
  shows the first 20 lines of the text and its size against the limit of 64 KiB.
  It also says how many passages will be embedded and with which model, for
  example "About 4 passages will be embedded with granite-embedding-107m-multilingual".
  The count is an estimate from the size of the text and the chunking settings
  (`max_chunk_size` and `chunk_overlap` in `GET /api/collections`).
  You can edit the text before you confirm. A change waits 24 hours, and you can
  have 10 waiting and an agent can make 5 an hour, as for the other kinds.
- **A scan for secrets.** Before a text is shown, LocalAI checks it with the same
  patterns that keep a memory note from being saved at once (private keys, access
  tokens, `password: ...` lines). A text that matches is not refused: it shows
  with a warning in the card, and you can still confirm it in the web interface.
  A messaging chat cannot confirm it. The bot writes nothing about it in the
  chat, a `yes` there does not confirm it, and a `no` still drops it. The proposal
  carries the code in `warning` (`secret`).
- **What happens.** **Confirm** stores the text as one entry of the collection,
  titled with the summary, and the server splits it into passages and embeds it.
  **Undo** deletes that entry again. The embedding model must be installed;
  without it the kind is not offered.
- **Messaging apps.** `knowledge.write` is also a permission of a connection. It
  is sensitive: only a sender you paired can use it, never a sender who is only
  on the allowlist or in a listed channel. The bot writes the proposal in the
  chat with the collection and the first lines of the text, and the paired sender
  answers `yes`, `no` or `undo` there. The connection page explains a block with
  the usual reasons, plus `knowledge_none` (the agent has no collection) and
  `knowledge_no_embedding` (the embedding model is not installed).

This first version takes a text. Following a URL or adding a file of your Files
folder by proposal is not available yet; add those yourself on the Knowledge page.
