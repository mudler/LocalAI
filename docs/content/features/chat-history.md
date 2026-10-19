+++
disableToc = false
title = "Chat History"
weight = 49
url = "/features/chat-history/"
+++

Chat history keeps the conversations of the web UI [Chat]({{%relref "features/chat" %}}) page on the server, per user, so they survive a browser reset and follow you to another device. It is built on the [user authentication system]({{%relref "features/authentication" %}}): conversations live in the same database as users, sessions and API keys (SQLite by default, PostgreSQL when `LOCALAI_AUTH_DATABASE_URL` points at one).

{{% notice note %}}
Chat history is only active when user authentication is enabled (`LOCALAI_AUTH=true`). Without it, or with only legacy API keys, the endpoints below are not registered and the web UI keeps conversations in the browser (`localai_chats_data` in `localStorage`), as before. With it, the browser copy is kept per account (see [One browser cache per account](#one-browser-cache-per-account)). Disabling the web UI (`LOCALAI_DISABLE_WEBUI=true`) also disables chat history.
{{% /notice %}}

## Permission

Access is controlled by the `chat_history` feature permission. Like the other API features it is **on by default** for new users; an admin can turn it off per user under **Settings → Users and keys**. A user without it gets `403` from every endpoint, and their web UI falls back to browser storage.

## Deletions are final

A deleted conversation must not come back because another device still has a copy. The server therefore keeps a **tombstone** for every deleted conversation: the row stays, its content is dropped, and it is marked deleted.

- Writing a conversation that has a tombstone returns `410 Gone`. Nothing is written.
- `GET /api/conversations` lists tombstones under `deleted`, so a client can drop its local copies of those ids instead of uploading them again.
- `DELETE` is idempotent, and deleting an id the server has never seen still records a tombstone. A delete that overtakes an in-flight save of the same conversation therefore still wins.
- Tombstones are per user. Another user can use the same id.

Every conversation also carries a server-assigned `version`: `1` after the first save, then one more for every write, including the delete. A client that has a non-zero version for a conversation knows the server has seen it; a conversation without one is a draft that was never uploaded.

Within a live conversation the last write wins: there is no merge of two devices editing the same conversation at the same time. `createdAt` and `updatedAt` (Unix milliseconds) are stored as the client sends them, so clients can compare `updatedAt` to pick the newer copy; the server only fills them in when they are `0`.

## How the web UI syncs

On load the Chat page asks the server for the list and reconciles it with what the browser has:

- A conversation the server reports as deleted is removed locally.
- A conversation on both sides keeps the copy with the newer `updatedAt`. A message you send while the list is still loading is kept, and uploaded afterwards.
- A conversation only in the browser is uploaded if it has never been synced (no `version`). If it has a version but the server no longer has it, it is removed locally: it was synced before, so the server dropped it (or the browser now belongs to another account).
- A conversation only on the server is added.
- A conversation you delete while the list is still loading is deleted on the server once the list arrives.

After that, every change is saved on its own (debounced, only conversations that changed). When a save returns `410`, the UI removes that conversation locally. On any other error it keeps the browser copy and tries again with the next change. `localStorage` stays a cache of the last state either way. The web UI does not use the bulk endpoint: every upload goes through the per-conversation save, so the tombstone check applies to each one.

### Deletes wait for the server

A delete (one conversation, **Clear all**, or a delete on the Home page) is removed from the screen at once, and kept in the browser as a pending delete until the server confirms it. If the server cannot be reached, the delete is sent again with the next change, when the browser comes back online, and on the next load; until then the list from the server cannot bring the conversation back. While a **Clear all** is pending, no conversation is uploaded, so a chat started after it is not caught by it.

Clearing all chats immediately updates the browser cache, including unsynced drafts. You can leave the page without waiting for the server to confirm deletion.

### One browser cache per account

The browser cache belongs to the signed-in account: it is stored under `localai_chats_data:<user id>`, together with that account's pending deletes. When one account signs out and another signs in on the same browser, the second account starts from its own cache and its own server list. A conversation the first account could not upload yet (for example because its first save failed) stays in the first account's cache and is uploaded when that account signs in again; it is never uploaded into another account. Signing out does not delete the cache.

Conversations stored in the browser before chat history was enabled live under the shared key `localai_chats_data`. When you are signed in, the Chat page does not read that key and does not upload it, because the browser cannot tell which account it belongs to. Without user authentication the web UI keeps using it, as before.

## API

All endpoints act on the conversations of the calling user and need a session or API key.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/conversations` | Live conversations (`conversations`, newest first) and tombstones (`deleted`) |
| `POST` | `/api/conversations` | Save (upsert) the conversation in the body |
| `PUT` | `/api/conversations/{id}` | Save (upsert); the path id overrides the body id |
| `GET` | `/api/conversations/{id}` | One conversation; `404` if it never existed, `410` if it was deleted |
| `DELETE` | `/api/conversations/{id}` | Delete, leaving a tombstone. Idempotent |
| `DELETE` | `/api/conversations` | Delete all of the user's conversations, leaving tombstones |
| `PUT` | `/api/conversations/bulk` | Make the payload the user's live set: upsert each conversation, delete (tombstone) the others. Ids that were deleted earlier are not written and come back in `skipped` |

Status codes: `400` for a malformed id or body, `401` without credentials, `403` without the `chat_history` permission, `409` when concurrent writers kept racing on the same id (retry), `410` for a deleted conversation.

Conversation ids are 1 to 128 characters from `A-Z a-z 0-9 . _ -`. A conversation carries what the Chat page keeps per chat: name, model, system prompt, sampling settings, MCP and LocalAI Assistant settings, token usage and context size (`schema.Conversation` in `/swagger`). The message list (`history`) is stored as sent, so every message shape the UI uses (text, images, audio, files, tool calls, thinking) survives the round trip.

### Example

```bash
# Save a conversation
curl -X PUT http://localhost:8080/api/conversations/3f9c2a \
  -H "Authorization: Bearer $LOCALAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"id":"3f9c2a","name":"Release notes","model":"qwen3-8b",
       "history":[{"role":"user","content":"Summarise the changes"}],
       "updatedAt":1760342400000}'
# -> {"id":"3f9c2a", ..., "createdAt":1760342400000, "updatedAt":1760342400000, "version":1}

# Delete it, then try to write it again from a stale client
curl -X DELETE http://localhost:8080/api/conversations/3f9c2a -H "Authorization: Bearer $LOCALAI_API_KEY"
curl -X PUT http://localhost:8080/api/conversations/3f9c2a \
  -H "Authorization: Bearer $LOCALAI_API_KEY" -H "Content-Type: application/json" \
  -d '{"id":"3f9c2a","name":"Release notes"}'
# -> 410 {"error":"conversation was deleted"}

curl http://localhost:8080/api/conversations -H "Authorization: Bearer $LOCALAI_API_KEY"
# -> {"conversations":[],"deleted":[{"id":"3f9c2a","version":2,"deletedAt":1760342460000}]}
```

The machine-readable description is under the `chat-history` tag in the [API discovery]({{%relref "features/api-discovery" %}}) instructions (`GET /api/instructions/chat-history`) and in `/swagger`.
