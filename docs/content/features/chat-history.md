+++
disableToc = false
title = "Chat History"
weight = 49
url = "/features/chat-history/"
+++

Chat history keeps the conversations of the web UI [Chat]({{%relref "features/chat" %}}) page on the server, per user, so they survive a browser reset and follow you to another device. It is built on the [user authentication system]({{%relref "features/authentication" %}}): conversations live in the same database as users, sessions and API keys (SQLite by default, PostgreSQL when `LOCALAI_AUTH_DATABASE_URL` points at one).

{{% notice note %}}
Chat history is only active when user authentication is enabled (`LOCALAI_AUTH=true`). Without it, or with only legacy API keys, the endpoints below are not registered and the web UI keeps conversations in the browser (`localai_chats_data` in `localStorage`), as before. Disabling the web UI (`LOCALAI_DISABLE_WEBUI=true`) also disables chat history.
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

Conversation ids are 1 to 128 characters from `A-Z a-z 0-9 . _ -`. The message list (`history`) is stored as sent, so every message shape the UI uses (text, images, audio, files, tool calls, thinking) survives the round trip.

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
