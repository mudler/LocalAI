
+++
disableToc = false
title = "Model Aliases"
weight = 14
url = "/features/model-aliases/"
+++

A **model alias** is a model name that redirects all traffic to another
configured model. Declare `gpt-4` as an alias of `my-llama-3` and every client
calling `gpt-4` is served by `my-llama-3` with no client reconfiguration: the
clients keep their existing model name while you control what answers them on
the server side.

## Declaring an alias

Create a minimal config file in your models directory:

```yaml
name: gpt-4
alias: my-llama-3
```

That is the whole config: a `name` (the alias clients call) and an `alias` key
(the target that actually serves the request).

## Rules and behavior

- The target (`my-llama-3`) must be an existing, non-alias, enabled model. You
  cannot point an alias at a missing model, a disabled model, or another alias
  (no chains).
- Aliases are 1:1. One alias maps to exactly one target.
- The target can be swapped live by editing the config file, calling the API,
  using the UI, or asking the assistant. No restart is required.
- Both `gpt-4` and `my-llama-3` appear in `GET /v1/models`.
- Responses echo the requested alias: a call to `gpt-4` returns `gpt-4` in the
  response `model` field, not the target name.
- Usage accounting records both sides: requested `gpt-4`, served `my-llama-3`.
- Aliases work for every modality (chat, embeddings, audio, images, and so on).

## Managing aliases

You can create, swap, and remove aliases from any of the management surfaces.

### Web UI

Open **Add Model** and pick the **Alias / Routing** template, then set a name
and a target. To re-point an existing alias, edit it and change the target.

### REST API

- Create: `POST /models/import`
- Swap the target: `PATCH /api/models/config-json/:name`
- List all aliases: `GET /api/aliases`
- Delete: `POST /models/delete/:name`

### Assistant and MCP

The LocalAI Assistant (and the MCP server) expose the same operations as tools:
`set_alias`, `list_aliases`, and `delete_model`.

{{% notice note %}}
**You cannot turn an existing real model into an alias.** If you run `set_alias`
(or `PATCH /api/models/config-json/:name`) against a name that is already a real,
non-alias model, the request is **rejected**. An alias is a pure redirect, so it
must not carry a `backend` or `parameters.model`; a real model does, and merging
an `alias` onto it produces an invalid config that validation refuses with
`alias config ... must not set backend or parameters.model`. This is intentional:
it stops a stray `set_alias` call from clobbering a model that is serving.

To add an alias, point a **new** name at the target instead of reusing an
existing model's name. Re-pointing an **existing alias** at a different target
is fully supported and is the live-swap path: the alias config has no backend of
its own, so swapping its target stays a valid pure redirect.
{{% /notice %}}

## Limits

Aliases are a static 1:1 redirect. For classifier-based or load-balanced
selection across several downstream models, use the intelligent router in the
[Middleware]({{%relref "operations/middleware" %}}) feature instead.
