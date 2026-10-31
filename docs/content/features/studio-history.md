+++
disableToc = false
title = "Studio History"
weight = 49
url = "/features/studio-history/"
+++

Studio history keeps the record of every [Studio]({{%relref "features/studio" %}}) generation (prompt, model, settings, the result's URL and the lineage between results) on the server, per user, so it survives a browser reset and follows you to another device. It is built the same way as [Chat History]({{%relref "features/chat-history" %}}): entries live in the same database as users, sessions and API keys (SQLite by default, PostgreSQL when `LOCALAI_AUTH_DATABASE_URL` points at one).

Only the records move. The generated files stay where the generation endpoints write them (`/generated-images`, `/generated-videos`, `/generated-audio`, `/generated-3d` under the generated content directory), as before.

{{% notice note %}}
Studio history is only active when user authentication is enabled (`LOCALAI_AUTH=true`). Without it the list answers `{"enabled": false}` (the other endpoints are not registered) and the Studio keeps its history in the browser (`localai_*_history` in `localStorage`), as before. Disabling the web UI (`LOCALAI_DISABLE_WEBUI=true`) also disables Studio history. 3D results still live in the browser's IndexedDB in this version: a 3D entry holds the model itself (a GLB file of several megabytes) and its input thumbnail as blobs, not URLs, so syncing only its record would show it on another device without the model. The server already accepts `threed` entries for when the 3D page keeps its models as files.
{{% /notice %}}

## Permission

Access is controlled by the `studio_history` feature permission. Like the other API features it is **on by default** for new users; an admin can turn it off per user under **Settings → Users and keys**. A user without it gets `403` from every endpoint, and their Studio falls back to browser storage.

## How the Studio syncs

On load the Studio asks the server for the list once and merges it with what the browser has:

- An entry the server reports as deleted is removed locally.
- An entry on both sides takes the server's copy.
- An entry only in the browser is uploaded if it was never synced. If it was synced before and the server no longer has it, it is removed locally (it was deleted on another device, or trimmed).
- An entry only on the server is added.

After that, every new result is uploaded as it is made, and a delete or **Clear** goes to the server too. A delete the server could not take is kept in the browser and sent again with the next load or when the browser comes back online; until then the entry stays hidden even if the server still lists it. A **Clear** that could not reach the server is kept as the list of entries it removed, so sending it later never deletes results made on another device in the meantime.

### One browser cache per account

The Studio is not told who is signed in, so the list carries an opaque `cacheKey` for the account (a hash of the user id). The browser cache is kept under `localai_<type>_history:<cacheKey>`. Until the server has answered, the Studio shows no history rather than risk showing another account's: when two people share a browser, each sees only their own work.

On a browser that has held an account's history, a server that answers with anything but `404` (signed out, the `studio_history` permission revoked, offline) leaves the account unknown. The Studio then shows no history and keeps none in the browser; results made meanwhile are visible on the page until it is reloaded. A list with `enabled: false` (or a `404`) means this LocalAI keeps no history (no user authentication), and the browser is the record again.

### History from before

The entries a browser kept before Studio history was enabled (under the plain `localai_<type>_history` keys) are imported into the **first** account that signs in on that browser, once, with `POST /api/studio/history/import`. After a successful import they are removed from the plain keys (they now live on the server and in that account's cache), and the browser marks the import as done, so a second account never receives them. The import never deletes anything on the server.

## Limits

- Each user keeps up to 1000 entries per type. Saving one more turns the oldest of that type into a tombstone, so the trim reaches every device. The browser cache keeps the newest 100 per type.
- One entry is at most 64 KiB of JSON. The Studio already cuts prompts at 2000 characters and stores file URLs, never the media.
- Entry ids are 1 to 128 characters from `A-Z a-z 0-9 . _ -`. An entry keeps the type of its first save.

## API

All endpoints act on the entries of the calling user and need a session or API key.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/studio/history?type=` | `enabled`, live entries (`entries`, newest first), tombstones (`deleted`) and the account's `cacheKey`; all types without `type`. Without user authentication: `200` with `enabled: false` and empty lists |
| `PUT` | `/api/studio/history/{id}` | Save (upsert) `{type, entry, createdAt, updatedAt}`; returns the entry with its `version` |
| `GET` | `/api/studio/history/{id}` | One entry; `404` if it never existed, `410` if it was deleted |
| `DELETE` | `/api/studio/history/{id}?type=` | Delete, leaving a tombstone. Idempotent; `type` is recorded when the server has not seen the entry yet |
| `DELETE` | `/api/studio/history?type=` | Clear one type, or every type without `type`, leaving tombstones |
| `POST` | `/api/studio/history/import` | Upsert `{entries: [...]}` without deleting anything; ids that were deleted earlier come back in `skipped` |

Types: `image`, `video`, `tts`, `sound`, `audio-transform`, `diarization`, `threed`. Status codes: `400` for a malformed id, type or body, `401` without credentials, `403` without the `studio_history` permission, `409` when concurrent writers kept racing on the same id (retry), `410` for a deleted entry.

`entry` is the Studio's own record of the result, stored as sent: `prompt`, `model`, `params`, `results` (each with the file's `url`), `elapsedMs`, and `parentId` / `edge` when the result was made from another one.

### Example: add a result made through the API

A script that generates a clip with `/video` can record it, so it appears in the Studio of the same user:

```bash
curl -X PUT http://localhost:8080/api/studio/history/robot-part1 \
  -H "Authorization: Bearer $LOCALAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"type":"video","createdAt":1793377463000,
       "entry":{"id":"robot-part1","createdAt":1793377463000,
                "prompt":"A small grey robot with four legs walks across a kitchen table",
                "model":"ltx-2.3-22b-distilled-ggml","params":{"size":"768x512"},"elapsedMs":148000,
                "results":[{"url":"/generated-videos/b642560649148.mp4"}]}}'
# -> {"id":"robot-part1","type":"video", ..., "version":1}

curl "http://localhost:8080/api/studio/history?type=video" -H "Authorization: Bearer $LOCALAI_API_KEY"
# -> {"entries":[{"id":"robot-part1", ...}],"deleted":[],"cacheKey":"..."}
```

The machine-readable description is under the `studio-history` tag in the [API discovery]({{%relref "features/api-discovery" %}}) instructions (`GET /api/instructions/studio-history`) and in `/swagger`.
