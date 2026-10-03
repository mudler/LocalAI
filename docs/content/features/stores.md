
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
Three backends ship with LocalAI:

| Backend | `backend` value | Persistence | Notes |
|---------|-----------------|-------------|-------|
| Local (default) | `local-store` (alias `embedded-store`) | In-memory, lost on restart | Exact cosine similarity, zero configuration. |
| Valkey Search | `valkey-store` (alias `valkey`) | Durable (Valkey RDB/AOF) | Backed by a Valkey Search (`FT.*`) server; survives restarts and supports opt-in HNSW. |
| Qdrant | `qdrant-store` (alias `qdrant`) | Durable (Qdrant storage) | Backed by a Qdrant server or cluster, or Qdrant Cloud; HNSW-indexed; on a single node a `find` right after a `set` sees the new vectors. |

Internal features that use stores (face and voice recognition, the router's KNN classifier and
embedding cache) don't send a `backend` field. To move the router's stores (`router-cache-<router>`
and `router-corpus-<router>`) to a durable backend, create a model config named after the store with
the `backend:` and `options:` shown below. Keep the default cosine `distance_metric` for them: they
compare similarities against cosine thresholds. Keep face and voice recognition on `local-store`
for now: their registries remember enrolled IDs only in memory, so after a restart entries in a
durable store could no longer be forgotten. With tracing enabled, the router's vector-store calls
appear in `/api/backend-traces` under the backend each store resolved to.

A `backend` field that names a different backend than the store's own (its model config's
`backend:`, or `local-store` when there is none) is served by a separate backend instance, so it
never lands on the store's usual backend. It gets the model config's `options:` only if the config
has no `backend:`; otherwise those options belong to the configured backend, and it gets its own
defaults.

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
  - username_env:MY_VALKEY_USER
  - password_env:MY_VALKEY_PASSWORD
  - index_algo:HNSW
  - distance_metric:COSINE
```

The `username_env` / `password_env` options name an environment variable that holds the actual
credential rather than putting the secret directly in the YAML. This mirrors `cloud-proxy`'s
`api_key_env` pattern and lets distinct store configs each reference their own credentials.
The direct `username` / `password` options still work for backward compatibility, and take
precedence when both are set.

When no config exists for a store, the backend connects to `localhost:6379` with the defaults
below (so the zero-config experience still works).

| Option | Default | Description |
|--------|---------|-------------|
| `addr` | `localhost:6379` | Valkey server address (`host:port`). |
| `username` | *(empty)* | Optional ACL username (plaintext in config). |
| `password` | *(empty)* | Optional password / ACL secret (plaintext in config). |
| `username_env` | *(empty)* | Name of an env var that holds the username. Preferred over `username` for secrets — keeps credentials out of the model YAML. |
| `password_env` | *(empty)* | Name of an env var that holds the password. Preferred over `password` for secrets. |
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

### Qdrant store backend

The `qdrant-store` backend keeps vectors in a [Qdrant](https://qdrant.tech/) collection, so the
data survives a LocalAI restart. It works with a single Qdrant node, a Qdrant cluster and Qdrant
Cloud. It talks to Qdrant's **gRPC** port (`6334` by default), not the `6333` REST port.

Select it by passing `"backend": "qdrant-store"` (or the `"qdrant"` alias) on any `/stores/*` request:

```
curl -X POST http://localhost:8080/stores/set \
     -H "Content-Type: application/json" \
     -d '{"backend": "qdrant-store", "store": "my-vectors", "keys": [[0.1, 0.2], [0.3, 0.4]], "values": ["foo", "bar"]}'
```

As with Valkey, the connection and collection are configured through a **model config** named
after the store, with the settings in `options:` as `key:value` strings:

```yaml
name: my-vectors
backend: qdrant-store
options:
  - addr:xyz-example.eu-central.aws.cloud.qdrant.io:6334
  - tls:true
  - api_key_env:QDRANT_API_KEY
  - distance_metric:COSINE
```

When no config exists for a store, the backend connects to `localhost:6334` with the defaults
below.

Each store gets its own collection, named `localai_<store>-<hash>` (for example
`localai_my-vectors-1a2b3c4d`) unless you set `collection`. It is created on the first `set` with
the dimension of the first key, and Qdrant rejects keys and queries of any other dimension, even
after every key is deleted. To change the dimension (for example after switching embedding
models), delete the collection in Qdrant or set a new `collection`. The backend never deletes
collections itself, since with several writers that could lose another writer's data.

Keys are matched exactly, and a repeated key keeps the last value. A `set` is a single Qdrant
request, so a rejected `set` writes nothing. `NaN` and `Inf` components are rejected.

| Option | Default | Description |
|--------|---------|-------------|
| `addr` | `localhost:6334` | Qdrant gRPC address (`host:port`; the port defaults to `6334`). Not a URL — use `tls:true` for HTTPS endpoints. |
| `api_key` | *(empty)* | Qdrant API key (plaintext in config). |
| `api_key_env` | *(empty)* | Name of an env var that holds the API key. Preferred over `api_key` — keeps the secret out of the model YAML. |
| `tls` | `false` | Enable TLS (required by Qdrant Cloud). |
| `tls_ca_cert` | *(empty)* | Path to a PEM CA bundle used to verify the server certificate (self-signed / private CA). Turns on `tls`. |
| `tls_skip_verify` | `false` | Skip TLS certificate verification. Insecure — for testing only. Turns on `tls`. |
| `collection` | *(derived)* | Use this exact collection name instead of the derived one. |
| `distance_metric` | `COSINE` | `COSINE`, `EUCLID`, `DOT` or `MANHATTAN` (case-insensitive). |
| `request_timeout_ms` | `5000` | Per-request timeout in milliseconds. |

For `COSINE` the returned `similarities` follow the local store's convention (`1.0` = identical,
`-1.0` = opposite). For `DOT` they are dot products (larger is closer). For `EUCLID` and `MANHATTAN`
they are distances, so smaller is closer, which is the opposite ordering to `COSINE`. In every case
results come back nearest-first. If a collection already exists with a different distance than
`distance_metric`, loading the store (or, if the collection appeared later, the first `find`) fails
rather than returning scores with the wrong meaning.

{{% notice note %}}
The backend creates collections with Qdrant's default settings. For custom settings (sharding and
replication on a cluster, HNSW parameters, on-disk vectors, quantization), create the collection
in Qdrant yourself and point `collection` at it: an existing collection is used as long as it has a
single unnamed vector with the configured `distance_metric`.
{{% /notice %}}

{{% notice note %}}
Small collections are searched exactly. Once Qdrant builds the HNSW index (when a segment passes
its indexing threshold, 10 MB of vectors per segment by default, so typically tens of thousands of
embeddings), `find` is approximate, with recall set by the collection's HNSW settings.
{{% /notice %}}

{{% notice warning %}}
`tls` defaults to `false` (plaintext). Set `tls:true` whenever Qdrant is not on `localhost` or an
API key is configured, otherwise the key and the stored vectors travel the network unencrypted.
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
