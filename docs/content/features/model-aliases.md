
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
(the target that actually serves the request). An alias may add an
optional [profile](#profile-aliases) block.

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

To serve a name from several models with automatic fallback, use a [failover
chain]({{%relref "features/model-failover" %}}).

## Profile aliases

An alias can also act as a **profile** of its target: a different assistant on
the same loaded model. Add an optional `profile` block; its fields are applied on
top of the target's config for requests to the alias:

| Field | Effect |
|-------|--------|
| `profile.system_prompt` | Replaces the target's system prompt. A system message sent by the client still takes precedence, as for any model. |
| `profile.mcp` (`remote`, `stdio`) | Replaces the target's MCP servers. |
| `profile.mcp.auto` | Enables the MCP servers on `/v1/chat/completions` without `metadata.mcp_servers`, when MCP is enabled and the caller has the `mcp` permission. See [MCP]({{%relref "features/mcp" %}}). |
| `profile.client_tools: drop` | Discards tools and `tool_choice` sent by the client. |
| `profile.evidence_check.numbers` | Marks answers that contain numbers without evidence in the request. See [Evidence check](#evidence-check). |
| `profile.evidence_check.note` | Text appended by the evidence check. Defaults to an English note. |
| `profile.evidence_check.sources` | Appends the URLs the request's MCP tools returned to an answer that names no URL. See [Source line](#source-line). |
| `profile.evidence_check.sources_label` | Label of the source line. Defaults to `Sources`. |
| `profile.evidence_check.urls` | Marks answers that name links neither the request nor a tool returned. See [Links not retrieved](#links-not-retrieved). |
| `profile.evidence_check.urls_note` | Text appended by the link check; `{urls}` is replaced by the links. Defaults to an English note. |
| `profile.require_tool` | Makes the first model call of a request call one of the profile's MCP tools. See [Requiring a tool call](#requiring-a-tool-call). |

```yaml
name: researcher
alias: my-llama-3
profile:
  system_prompt: You are a careful research assistant. Cite your sources.
  client_tools: drop
  mcp:
    auto: true
    remote: |
      {"mcpServers": {"search": {"url": "http://search.internal/mcp"}}}
```

Requests to `researcher` run on the **same backend process** as `my-llama-3`:
the model is not loaded a second time, however many profiles point at it. A
separate model config with its own `backend` and model file would load the
weights again. Fields the profile does not set are taken from the target.

The block is opt-in. Only fields inside `profile` are applied: top-level fields
on an alias (`system_prompt`, `mcp`, sampling parameters, templates, ...) are
still ignored, so an alias without a `profile` block behaves exactly as before.
A `profile` block on a model that is not an alias is rejected.

MCP sessions of a profile with its own servers are kept apart from the target's,
so profiles with different tool sets do not interfere with each other.
`GET /v1/mcp/servers/<alias>` (and the `prompts` and `resources` endpoints) report
the servers a chat request for the alias would use, so a profile's tool set can
be checked without calling the model.

### Evidence check

A profile with server-side MCP tools returns only the final answer to the
client; the tool calls and their results stay on the server. When the client
sends the conversation back on the next turn, that tool trail is missing, and a
model may answer a follow-up question with a number it never looked up instead
of calling the tool again. The evidence check marks such answers at the output,
independently of the prompt:

```yaml
name: bookkeeper
alias: my-llama-3
profile:
  mcp:
    auto: true
    remote: |
      {"mcpServers": {"ledger": {"url": "http://ledger.internal/mcp"}}}
  evidence_check:
    numbers: true
    # optional, defaults to the note below
    note: "_Note: this answer contains numbers that no tool returned in this turn — not verified._"
```

The rule is deterministic and applies to each request on its own:

1. If an MCP tool call succeeded on the server during this request, the answer
   is left unchanged. A call that failed (the tool could not be called or
   returned an error to LocalAI) delivers no evidence and does not count.
2. Otherwise every number in the final answer (a digit sequence, optionally with
   one `.` or `,` decimal part) is looked up among the numbers of the request as
   received: the profile's system prompt and the content of every system, user,
   assistant and tool message. `3,5` and `3.5` count as the same number.
3. If at least one number of the answer is not found, the note is appended after
   a blank line. In a streamed response it arrives as one additional content
   chunk before the final chunk.

Answers that hand tool calls to the client are not checked. The check is off
unless `numbers: true` is set, and `note` without `numbers: true` is rejected.
It flags, it does not prove: a number the client happened to mention is
accepted even if the answer uses it wrongly.

### Source line

A research profile usually asks the model in its system prompt to cite its
sources. A model can still answer from a search result without naming a single
link, and the client never sees the tool results that held them. With
`sources: true` LocalAI adds the links at the output:

```yaml
name: researcher
alias: my-llama-3
profile:
  mcp:
    auto: true
    remote: |
      {"mcpServers": {"search": {"url": "http://search.internal/mcp"}}}
  evidence_check:
    sources: true
    # optional, defaults to "Sources"
    sources_label: Quellen
```

Like the number check it acts on `/v1/chat/completions`. The rule is
deterministic and applies to each request on its own:

1. Only the results of MCP tool calls that succeeded on the server during this
   request are read. The client's messages and failed calls are not a source.
2. If the final answer already contains an `http://` or `https://` URL, it is
   left unchanged.
3. Otherwise the distinct URLs of those tool results are collected in the order
   they first appear, at most five, and appended after a blank line as one line:
   `Quellen: https://a.example/test, https://b.example/oil`. Punctuation that
   ends a sentence is not taken as part of a URL. Parentheses inside URL paths
   and query parameters in JSON-encoded MCP text results are preserved.
4. If no tool result contains a URL, nothing is appended. No URL is ever made
   up: every link on the line was returned by a tool.

In a streamed response the line arrives as one additional content chunk before
the final chunk, like the number note. Both checks can be enabled together; the
number note follows the source line. `sources_label` without `sources: true` is
rejected.

### Links not retrieved

A model can also name a link it never opened: a plausible deep link on a known
site, written from memory. The client cannot tell such a link from one a search
tool returned, because it never sees the tool results. With `urls: true` LocalAI
marks it at the output:

```yaml
name: researcher
alias: my-llama-3
profile:
  mcp:
    auto: true
    remote: |
      {"mcpServers": {"search": {"url": "http://search.internal/mcp"}}}
  evidence_check:
    urls: true
    # optional; {urls} is replaced by the links, defaults to the note below
    urls_note: "_Note: this answer names links that no tool returned in this turn — not retrieved: {urls}_"
```

Like the other checks it acts on `/v1/chat/completions`, streamed or not. The
rule is deterministic and applies to each request on its own:

1. Every `http://` or `https://` URL in the final answer is collected, bare or
   inside a Markdown link. Punctuation that ends a sentence is not part of it.
2. Evidence is every URL in the request as received — the profile's system
   prompt and the content of every system, user and tool message — and in the
   results of the MCP tool calls that succeeded on the server during this
   request. Assistant messages are not evidence, so a link the model invented on
   an earlier turn is marked again when the client sends it back. Failed tool
   calls deliver no evidence.
3. Links are compared without scheme, fragment, a leading `www.` and a trailing
   slash; the host is compared case-insensitively, path and query exactly. A
   link counts as retrieved when it equals an evidence URL or is a shorter form
   of one at a `/` or `?` boundary: `https://a.example` is backed by a retrieved
   `https://a.example/report.pdf`, but `https://a.example/other.pdf` is not.
4. If at least one link is not backed, the note is appended after a blank line
   with up to five such links. In a streamed response it arrives as one
   additional content chunk before the final chunk. If `urls_note` contains no
   `{urls}`, the links follow the note after a space.

The source line, this note and the number note can be enabled together and are
appended in that order. `urls_note` without `urls: true` is rejected. The check
flags, it does not prove: a link that was retrieved can still be cited for a
claim the page does not make.

### Requiring a tool call

A system prompt that says "always search first" does not stop a model from
answering a question from its own knowledge, for example when the question comes
with a picture. `require_tool: true` enforces the first step in the request
instead:

```yaml
name: researcher
alias: my-llama-3
profile:
  require_tool: true
  mcp:
    auto: true
    remote: |
      {"mcpServers": {"search": {"url": "http://search.internal/mcp"}}}
```

When the switch is set and a `/v1/chat/completions` request offers the
profile's server-side MCP tools, the **first** model call of the request is made with
`tool_choice: "required"`. Every follow-up call of the server-side tool loop is
made with the `tool_choice` the client sent (or none), so after the tool results
the model is free to answer. A `required` that stayed in place would make the
model call a tool again after every result and never answer until the loop's
iteration limit.

How `required` is enforced depends on the model:

- With `template.use_tokenizer_template` (llama.cpp with the model's own chat
  template), LocalAI passes `tool_choice: "required"` to the backend, which
  constrains the output to a tool call with a grammar built from the chat
  template's tool-call format.
- When LocalAI builds the tool-call grammar itself, the first call's grammar
  leaves out the no-action function (`answer`) the model would otherwise reply
  with directly.

The first call is not forced when:

- no server-side MCP tool was offered (MCP disabled, the caller lacks the `mcp`
  permission, or the profile has no servers),
- the client set `tool_choice` (other than `auto`) or `function_call` itself,
  which keeps precedence,
- the request does not end with a user message. A request that ends with a
  `tool` message returns the result of a tool the client ran itself; forcing
  another call there could bounce tool calls between client and server.

The switch applies per request, so every new user turn starts with a tool call.
It enforces that a tool is called, not which one or with which arguments.

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

## Aliases as deployment slots (distributed mode)

In [distributed mode]({{%relref "features/distributed-mode" %}}) an alias can
carry a scheduling rule. `POST /api/nodes/scheduling` accepts an alias for
`model_name`, and the rule then governs whatever model the alias points at:

```bash
curl -X POST http://frontend:8080/api/nodes/scheduling \
  -H "Content-Type: application/json" \
  -d '{"model_name": "production", "node_selector": {"tier": "gpu"}, "min_replicas": 2}'
```

Re-point `production` and the placement policy follows it, so the alias behaves
as a stable slot whose contents you can swap. Because a replica is shared by
every name that resolves to it, only one rule may govern a given model at a
time. See [Scheduling a model alias]({{%relref "features/distributed-mode" %}}#scheduling-a-model-alias).

## Limits

Aliases are a static 1:1 redirect; a profile block changes only the fields listed
above, not the model or its loading options. For classifier-based or load-balanced
selection across several downstream models, use the intelligent router in the
[Middleware]({{%relref "operations/middleware" %}}) feature instead.
