
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
     -d '{"backend": "valkey-store", "store": "my-vectors", "keys": [[0.1, 0.2], [0.3, 0.4]], "values": ["foo", "bar"]}'
```

The connection and index are configured through a **model config** named after the store (the
`store` field on the request, which is the store's model ID). Create a YAML in your models
directory whose `name` matches the store, set `backend: valkey-store`, and put the connection /
index settings in the `options:` list as `key:value` strings. Because each store resolves its own
config, different stores can point at different Valkey servers or use different index settings
within one LocalAI process:

```yaml
name: my-vectors
backend: valkey-store
options:
  - addr:valkey.internal:6379
  - index_algo:HNSW
  - distance_metric:COSINE
```

When no config exists for a store, the backend connects to `localhost:6379` with the defaults
below (so the zero-config experience still works).

| Option | Default | Description |
|--------|---------|-------------|
| `addr` | `localhost:6379` | Valkey server address (`host:port`). |
| `username` | *(empty)* | Optional ACL username. |
| `password` | *(empty)* | Optional password / ACL secret. |
| `tls` | `false` | Enable TLS (required by many managed deployments). |
| `tls_ca_cert` | *(empty)* | Path to a PEM CA bundle used to verify the server certificate (self-signed / private CA). |
| `tls_skip_verify` | `false` | Skip TLS certificate verification. Insecure — for testing only. |
| `client_name` | `localai-valkey-store` | Connection name reported by `CLIENT LIST`. Always set. |
| `db` | `0` | Logical Valkey DB index (`SELECT n`). Namespace prefixing already isolates keyspaces on a shared DB. |
| `index_algo` | `FLAT` | `FLAT` (exact, default) or `HNSW` (approximate ANN for large corpora). |
| `hnsw_m` | `16` | HNSW graph degree (only when `index_algo:HNSW`). |
| `hnsw_ef_construction` | `200` | HNSW build-time candidate list (HNSW only). |
| `hnsw_ef_runtime` | `10` | HNSW query-time candidate list (HNSW only). |
| `distance_metric` | `COSINE` | `COSINE` (default), `L2` or `IP`. |
| `request_timeout_ms` | `5000` | Per-command timeout in milliseconds. |

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
`tls` defaults to `false` (plaintext). Set `tls:true` whenever the Valkey server is
not on `localhost` or a `password`/`username` is configured, otherwise the credentials
and the stored vectors travel the network unencrypted. The TLS `ServerName` (SNI) is derived from
the host portion of `addr`, so certificate verification works for both hostname and
IP-addressed endpoints. For a self-signed / private CA, point `tls_ca_cert` at the PEM
bundle; `tls_skip_verify:true` disables verification entirely and should only be used for
local testing.
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
