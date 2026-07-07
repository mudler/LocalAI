
+++
disableToc = false
title = "Stores"
weight = 62
url = '/stores'
+++

Stores are an experimental feature to help with querying data using similarity search. It is
a low level API that consists of only `get`, `set`, `delete` and `find`.

{{% notice tip %}}
**Face recognition uses this store.** The 1:N face identification flow
(`/v1/face/register`, `/v1/face/identify`, `/v1/face/forget`) is built
on top of the generic store - see
[Face Recognition](/features/face-recognition/) for the face-oriented
API.
{{% /notice %}}

For example if you have an embedding of some text and want to find text with similar embeddings.
You can create embeddings for chunks of all your text then compare them against the embedding of the text you
are searching on.

An embedding here meaning a vector of numbers that represent some information about the text. The
embeddings are created from an A.I. model such as BERT or a more traditional method such as word
frequency.

Previously you would have to integrate with an external vector database or library directly.
With the stores feature you can now do it through the LocalAI API. 

Note however that doing a similarity search on embeddings is just one way to do retrieval. A higher level
API can take this into account, so this may not be the best place to start.

## API overview

There is an internal gRPC API and an external facing HTTP JSON API. We'll just discuss the external HTTP API,
however the HTTP API mirrors the gRPC API. Consult `pkg/store/client` for internal usage.

Everything is in columnar format meaning that instead of getting an array of objects with a key and a value each. 
You instead get two separate arrays of keys and values.

Keys are arrays of floating point numbers with a maximum width of 32bits. Values are strings (in gRPC they are bytes).

The key vectors must all be the same length and it's best for search performance if they are normalized. When
addings keys it will be detected if they are not normalized and what length they are.

All endpoints accept a `store` field which specifies which store to operate on. Stores are created
on the fly. By default the in-memory `local-store` backend is used and no configuration is required,
but you can select a different store backend per request (see [Backends](#backends) below).

## Backends

Each `/stores/*` request accepts an optional `backend` field selecting the store implementation.
Two backends ship with LocalAI:

| Backend | `backend` value | Persistence | Notes |
|---------|-----------------|-------------|-------|
| Local (default) | `local-store` (alias `embedded-store`) | In-memory, lost on restart | Exact cosine similarity, zero configuration. |
| Valkey Search | `valkey-store` (alias `valkey`) | Durable (Valkey RDB/AOF) | Backed by a Valkey Search (`FT.*`) server; survives restarts and supports opt-in HNSW. |

### Valkey store backend

The `valkey-store` backend persists vectors in a [Valkey Search](https://valkey.io/) server, so
the data survives a LocalAI restart — unlike the in-memory default. It requires a reachable server
that ships the Valkey Search module (for example the `valkey/valkey-bundle` image).

Select it by passing `"backend": "valkey-store"` (or the `"valkey"` alias) on any `/stores/*` request:

```
curl -X POST http://localhost:8080/stores/set \
     -H "Content-Type: application/json" \
     -d '{"backend": "valkey-store", "keys": [[0.1, 0.2], [0.3, 0.4]], "values": ["foo", "bar"]}'
```

The connection and index are configured through environment variables read by the backend process:

| Variable | Default | Description |
|----------|---------|-------------|
| `VALKEY_ADDR` | `localhost:6379` | Valkey server address (`host:port`). |
| `VALKEY_USERNAME` | *(empty)* | Optional ACL username. |
| `VALKEY_PASSWORD` | *(empty)* | Optional password / ACL secret. |
| `VALKEY_TLS` | `false` | Enable TLS (required by many managed deployments). |
| `VALKEY_CLIENT_NAME` | `localai-valkey-store` | Connection name reported by `CLIENT LIST`. Always set. |
| `VALKEY_INDEX_ALGO` | `FLAT` | `FLAT` (exact, default) or `HNSW` (approximate ANN for large corpora). |
| `VALKEY_HNSW_M` | `16` | HNSW graph degree (only when `VALKEY_INDEX_ALGO=HNSW`). |
| `VALKEY_HNSW_EF_CONSTRUCTION` | `200` | HNSW build-time candidate list (HNSW only). |
| `VALKEY_HNSW_EF_RUNTIME` | `10` | HNSW query-time candidate list (HNSW only). |
| `VALKEY_DISTANCE_METRIC` | `COSINE` | `COSINE` (default), `L2` or `IP`. |
| `VALKEY_REQUEST_TIMEOUT_MS` | `5000` | Per-command timeout in milliseconds. |

For `COSINE` the returned `similarities` follow the same convention as the local store
(`1.0` = identical, `-1.0` = opposite); internally Valkey returns a cosine *distance* which the
backend converts with `similarity = 1 - distance`. For `L2` and `IP` the raw Valkey score is
returned in `similarities` (for `L2`, smaller means closer — the opposite ordering of `COSINE`);
nearest-first ordering of the results is preserved in all cases.

{{% notice note %}}
Valkey Search updates its vector index **asynchronously** after a write. A `find` issued
immediately after `set` may not yet see the new vectors — poll `find` briefly (or retry) until the
expected results appear. `get` and `delete` are synchronous and unaffected.
{{% /notice %}}

{{% notice note %}}
This backend targets a **standalone** Valkey Search server (one server per namespace/model). Valkey
Cluster is not a supported target yet — index coordination across shards is out of scope for this
backend.
{{% /notice %}}

{{% notice warning %}}
`VALKEY_TLS` defaults to `false` (plaintext). Set `VALKEY_TLS=true` whenever the Valkey server is
not on `localhost` or a `VALKEY_PASSWORD`/`VALKEY_USERNAME` is configured, otherwise the credentials
and the stored vectors travel the network unencrypted.
{{% /notice %}}

## Set

To set some keys you can do

```
curl -X POST http://localhost:8080/stores/set \
     -H "Content-Type: application/json" \
     -d '{"keys": [[0.1, 0.2], [0.3, 0.4]], "values": ["foo", "bar"]}'
```

Setting the same keys again will update their values.

On success 200 OK is returned with no body.

## Get

To get some keys you can do

```
curl -X POST http://localhost:8080/stores/get \
     -H "Content-Type: application/json" \
     -d '{"keys": [[0.1, 0.2]]}'
```

Both the keys and values are returned, e.g: `{"keys":[[0.1,0.2]],"values":["foo"]}`

The order of the keys is not preserved! If a key does not exist then nothing is returned.

## Delete

To delete keys and values you can do

```
curl -X POST http://localhost:8080/stores/delete \
     -H "Content-Type: application/json" \
     -d '{"keys": [[0.1, 0.2]]}'
```

If a key doesn't exist then it is ignored.

On success 200 OK is returned with no body.

## Find

To do a similarity search you can do

```
curl -X POST http://localhost:8080/stores/find 
     -H "Content-Type: application/json" \
     -d '{"topk": 2, "key": [0.2, 0.1]}'
```

`topk` limits the number of results returned. The result value is the same as `get`,
except that it also includes an array of `similarities`. Where `1.0` is the maximum similarity.
They are returned in the order of most similar to least.
