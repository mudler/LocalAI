+++
url = "/features/chats-long-conversations/"
disableToc = false
title = "Long conversations"
description = "Keep a long chat going: automatic compaction, compacting or clearing the context yourself, and the context window a chat needs."
weight = 16
devbuild = "feature"
+++

Keep a long chat going. When a chat fills the context window of its model, LocalAI compacts the older part, and you can compact or clear the context yourself. The transcript keeps every message. For the compression policy of a model, see [Context compression]({{% relref "/features/chats-and-agents/context-compression" %}}).

## Long conversations

When a chat fills the model's context window, the older part is
compacted so the model can go on. The transcript keeps every message; only what
the model sees shrinks. A `compacted` event reports the size before and after.
The window comes from `context_size` in the model's configuration.

### Reasoning of earlier turns

A chat asks the model's chat template to keep the reasoning of earlier turns
(`chat_template_kwargs.preserve_thinking`). Some templates, such as the Qwen
3 family, otherwise keep the reasoning of the last turn only. A new turn then
renders the earlier answers without their reasoning, the prompt changes from
the first answer of the turn before, and a backend with a prompt cache
(llama.cpp) must read everything after that point again before the reply
starts. A template that does not know the argument ignores it. To turn it off
for a model, set `chat_template_kwargs: {preserve_thinking: false}` in its
configuration. A model served by a remote proxy is not asked.

### The context window a chat needs

Before you type, a chat already sends a fixed block to the model: the system
message, the always-on tools, the LocalAI tools (or the three tool-search
tools when they are deferred) and the chat template. That block is about 7,000
tokens for a regular user and more for an administrator, and the chat loop
needs at least 1,024 tokens free for the reply. A model with a smaller window
fails every turn with the failure code `context_too_small`, and so would every
new chat. A window that is only just large enough makes the chat compact its
conversation on every turn.

| Who | Minimum | Recommended |
|-----|---------|-------------|
| Regular user | 16384 | 32768 |
| Administrator (the chat has the LocalAI management tools) | 24576 | 32768 |

You do not have to do anything for a model that sets no `context_size`. When
the model can serve chats, LocalAI aims at 32768 tokens, capped by the
window the model was trained for, and steps down through 24576 and 16384 (and
only then 8192, 6144 and 4096) when the next window does not fit the memory of
the machine. Embedding, rerank and other models that cannot serve a chat keep
the smaller automatic window of 8192.

A `context_size` that you set is never changed, even when it is below the
minimum: it stays as you wrote it. The chat tells you instead. When the window
of the chat's model is below the minimum, a banner appears above the message
box, and `GET /api/chats/{id}` returns a `harness` object with `ok`, `window`,
`minimum`, `recommended`, `can_fix`, `admin` and `source`.

- An administrator sees whether the recommended window fits the memory of the
  machine and a button that sets the model's `context_size` to it, or to the
  largest step that fits and reaches the minimum. When nothing fits, the banner
  suggests a smaller model.
- A regular user cannot change a model. The banner lists the installed models
  whose window is large enough, to move the chat with one click, or says to ask
  an administrator.
- `source` is `model` when the model's window is too small, and `chat` when the
  chat's own `context_size` setting is below the minimum. The owner of the chat
  can change that setting.

Changing the `context_size` of a model needs an administrator; the server
refuses it for anyone else, whatever the page shows. A turn that fails because
the window is too small ends with the failure code `context_too_small` and a
message that names the window and the minimum.

### Compact or clear the context yourself

You do not have to wait for the window to fill. Two calls change what the model
sees from the next turn on; both answer 409 while a turn runs, and the
transcript keeps every message either way.

```bash
# Summarize the older part now, as the chat does when the window fills. It
# takes a while: the model writes the summary.
curl -X POST .../api/chats/3f1c.../compact
# {"before":18250,"after":4120}

# Start fresh in the same chat. Its files, title and switches stay.
curl -X POST .../api/chats/3f1c.../clear
# {"cleared":true}
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

- Compact keeps the last messages as they are and puts a summary in place of
  the ones before. It answers 400 when the conversation is too short to
  compact. The transcript gets a line with `role` `compacted` and the sizes in
  `before` and `after` (tokens, the same estimate as the `compacted` event).
- Clear writes a line with `role` `cleared`. From then on the model sees
  nothing above it. It answers 400 when the model sees no message to clear. A
  `cleared` event tells the other open pages to read the transcript again.
- A fork from a line above a `cleared` line keeps what the model saw there. A
  regenerate or an edit rebuilds what the model sees from the transcript after
  the last `cleared` line. The JSON export and the Markdown export show both
  lines, and an import keeps them.
- Both calls hold the chat the way a turn does, so in a cluster no frontend
  starts a turn on the chat while they run. Messages sent meanwhile wait and go
  when the call ends. Nothing is kept outside the chat store.
