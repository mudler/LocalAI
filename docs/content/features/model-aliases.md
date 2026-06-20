
+++
disableToc = false
title = "Model Aliases"
weight = 24
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
**Pointing at an existing real model converts it into an alias.** If you run
`set_alias` (or `PATCH /api/models/config-json/:name`) against a name that is
already a real, non-alias model, that model is turned into an alias of the
target. The operation is non-destructive (no data is deleted), but the model
stops serving with its own backend and starts redirecting to the target.

If you want to keep the original model serving as-is, point a **new** name at
the target instead of reusing an existing model's name.
{{% /notice %}}

## Limits

Aliases are a static 1:1 redirect. For classifier-based or load-balanced
selection across several downstream models, use the intelligent router in the
[Middleware]({{%relref "features/middleware" %}}) feature instead.
