+++
url = "/features/incognito/"
disableToc = false
title = "Incognito chats"
description = "Start a chat that does not use your memory, or one that the server does not keep at all."
weight = 41
devbuild = "feature"
+++

An incognito chat is a chat that the server does not list and does not keep after you leave it. You pick one of two levels:

| Level | What it does |
|---|---|
| **Memory off** | The chat does not read or save [memory]({{% relref "/features/chats-and-agents/short-memory" %}}). Logs, traces and usage stay as usual. |
| **Zero retention** | Memory off, and the server keeps no part of the chat. It keeps only the usage counters: user, model, token counts and time. |

Both levels apply to a plain chat, a chat with a [saved agent]({{% relref "/features/chats-and-agents/chats-agents" %}}) and Live voice in the chat.

## Start an incognito chat

1. On Home or on a new chat, press the **Incognito** button (the incognito icon) in the bottom bar of the message box, after **Live**, and turn on the switch.
2. Pick the level. **Memory off** is the default.
3. Send your message.

While the chat is incognito, the button becomes a pill that names the level, the message box takes a tint of the accent colour, and a line under it says what the level keeps. An incognito chat belongs to no project, so the project chip shows **No project**. The chat is not in the chats list or in the search.

The chat ends when you leave it: when you open another chat, go back to Home, close the tab, or turn **Incognito** off (the page asks first). The server deletes it then. If the page cannot tell the server, the server deletes the chat after one hour without use, and when it restarts.

## What each level covers

### Memory off

- The chat does not read your memory notes, the notes of its agent or the notes of a project.
- The chat cannot save a note or add text to Knowledge (your collections). Collections stay searchable.
- The chat itself is kept only in the memory of the server, and is never listed.
- Traces, logs, usage and the files that the chat or you write stay as usual.

### Zero retention

Everything of **Memory off**, and also:

| Where | What happens |
|---|---|
| Chat store | The transcript is kept only in the memory of the server, until the chat ends. |
| Traces | No API trace and no backend trace is recorded. |
| Usage | The usage row keeps user, model, token counts and time, with the `incognito` flag. It has no response metadata. Quotas and usage reports keep working. |
| Logs | The lines that print prompts, messages, tool arguments and answers show `[redacted: zero-retention]`, at every log level. While the request runs on a backend, the server writes `[redacted: zero-retention request in progress]` in place of each line of that backend's output, in its log and in the backend logs page. |
| Router cache | The embedding cache of a router model is read but gets no new entry. |
| Open Responses | The response is not stored. `previous_response_id` is refused: send the whole conversation instead. |
| Prompt cache | The `prompt_cache_path` of the model is not read or written for this request, and the backend is told not to keep any prompt cache on disk. |
| Generated media | Speech from Live voice is deleted from the generated-content directory after it is sent. Images are returned inline. |
| Tools | The chat gets only the tools that keep nothing: web search, web fetch, collection search, the search of past chats and the reading of context and skills. It cannot write files, run commands, start sub-agents, set triggers or save memory. The embedding call of a collection search keeps no trace either. When an administrator gives the collections their own API key (`LOCALAI_COLLECTIONS_API_KEY`), the search calls the API over the network and is refused in a zero-retention chat. |
| Attachments | Not accepted: an attachment is saved in your files. The chat page shows no attach button, no **Files** button and no preview pane. |

## What incognito does not cover

- **The model's memory in RAM.** A loaded model keeps a cache of recent prompts in RAM (the KV cache) to answer faster. It is not written to disk and it ends with the backend process, but until then it holds the prompt. Incognito does not clear it.
- **The output file of a backend process.** A backend can print what it receives, for example with a verbose or debug setting. The server hides those lines from its log and from the backend logs page while the request runs, but the backend writes them to an output file in its own temporary folder first, and that file is deleted only when the backend stops. In distributed mode the backends run on the worker nodes, whose logs the frontend does not control. Do not run backends in verbose mode if zero retention matters to you.
- **External services.** Web search and web fetch send your query to the search provider or the site. A remote model (a cloud proxy) sends the conversation to its provider. What they keep is up to them.
- **Your browser and your network.** Incognito does not hide the chat from someone who can see your screen or your network.
- **Explicit saves.** Enrolling a voice or a face, uploading to a collection or saving an agent happen on their own pages, which are not incognito.

## For administrators

The setting **Incognito chats** (`incognito`) is `allow` (default) or `forbid`:

- `allow`: Home and the chat page show the **Incognito** button.
- `forbid`: the button is hidden, and the server answers `403` to an incognito request from the web page, so the page never shows an incognito state that the server does not honour.

The setting **Highest incognito level** (`incognito_max_level`) is `zero-retention` (default) or `memory-off`. Pick `memory-off` when you need audit logs and traces of every chat: people can still keep chats out of memory, but not out of the logs. A request for a higher level gets `403`.

Change both in **Settings > Access and security**, or at start-up:

```bash
local-ai run --incognito=forbid
# or
LOCALAI_INCOGNITO=allow LOCALAI_INCOGNITO_MAX_LEVEL=memory-off local-ai run
```

Incognito is a feature of the web page only. The page marks its requests with an internal header, and the server honours it only for a web session (a session cookie, the key of the login page, or a server without accounts). A client that calls the API with an API key gets normal behaviour, whatever it sends. There is no per-key or per-user rule.

In distributed mode the model requests of an incognito chat carry the level to the worker nodes: the prompt cache path is cleared and the backend gets `NoPromptCache`. The logs and the temporary files of the worker nodes are outside the reach of the frontend.

With several frontends, an incognito chat lives in the memory of the frontend that created it. Route the requests of one browser session to the same frontend (sticky sessions); a request that reaches another frontend gets "not found", and the chat is lost.
