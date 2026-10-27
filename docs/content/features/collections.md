+++
disableToc = false
title = "Collections (knowledge base)"
weight = 21
url = "/features/collections/"
+++

A collection is a named set of documents that LocalAI splits into chunks and
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

The embedding calls go to the LocalAI API of the same server, inside the
process, so they need no API key and work with user accounts. The embedding
model must be installed. The default is `granite-embedding-107m-multilingual`.

## Who sees a collection

- With authentication off, there is one set of collections.
- With authentication on, each user has their own collections. A user needs the `collections` feature. Admins always have it.
- A name that is not yours answers `404`. Nobody sees another user's collections, with two exceptions for admins below.

## API

All paths are under `/api/collections`. While the service starts, every
path answers `503` with `collections are starting, please retry shortly`.
When collections are off, the paths do not exist.

| Method and path | Body | Result |
|---|---|---|
| `GET /` | | `{"collections": [...], "count": n}` |
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

## Use a collection in an agent

Add the names of your collections to the `collections` list of a
[saved agent](/features/chats-agents/). A chat with that agent can then search
only those collections. If a listed collection is deleted, the chat still starts
and the model is told that it is not available.
