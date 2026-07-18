
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
on the fly.

All endpoints also accept an optional `backend` field selecting which store backend serves the
request. The default is `local-store`, an in-memory exact-search store. Alternatively,
`valkey-store` (alias `valkey`) stores the vectors in a Valkey server running the
[Valkey Search](https://valkey.io/topics/search/) module (the `valkey/valkey-bundle` image), which
makes them durable across restarts. `valkey-store` is configured through environment variables,
or per-model via the model config's `options:` list, which takes priority over the env var:

| Variable | Model option | Default | Description |
|---|---|---|---|
| `VALKEY_ADDR` | `valkey_addr` | `127.0.0.1:6379` | Valkey server address |
| `VALKEY_USERNAME` | `valkey_username_env` | | Username, if the server requires AUTH |
| `VALKEY_PASSWORD` | `valkey_password_env` | | Password, if the server requires AUTH |
| `VALKEY_INDEX_ALGO` | `valkey_index_algo` | `FLAT` | Vector index algorithm: `FLAT` (exact, matches `local-store` results) or `HNSW` (approximate, for large corpora) |

The username/password options name an environment variable to read the credential from, rather
than taking the credential directly, so each model config can point at its own Valkey server with
its own credentials without putting secrets in the YAML (the same pattern `cloud-proxy`'s
`api_key_env` uses):

```yaml
name: my-valkey-store
backend: valkey-store
options:
  - "valkey_addr:valkey.internal:6379"
  - "valkey_username_env:MY_VALKEY_USERNAME"
  - "valkey_password_env:MY_VALKEY_PASSWORD"
  - "valkey_index_algo:HNSW"
```

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
