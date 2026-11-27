+++
disableToc = false
title = "Disk Prompt Cache"
weight = 101
description = "Keep the prompt cache of long llama.cpp prompts on disk, per user, so a model reload does not compute them again."
keywords = ["prompt cache","kv cache","llama.cpp","disk","retention","privacy"]
+++

The `llama-cpp` backend keeps the KV state of earlier prompts in RAM (the [server-side prompt cache]({{%relref "/models/capabilities/text/text-generation#server-side-prompt-cache-repeated-system-prompts" %}})). That cache is lost when the model unloads. The disk prompt cache keeps the state of long prompts on disk too. After a reload, a swap or a restart, a request with the same start reads the state from disk and does not compute it again.

The disk prompt cache is on by default. It needs a data path (`--data-path`, `LOCALAI_DATA_PATH`). With `LOCALAI_DATA_PATH=-` it is off.

## What is stored

Each entry holds:

- the prompt tokens, in clear. They can be read back as text;
- the KV state of the model for those tokens;
- for a hybrid model (Qwen3.5, LFM2, Granite-H), up to 3 checkpoints of the recurrent state.

Treat these files like stored chats. They contain what the users wrote, and the system prompts and tool results of their chats.

## Where it is stored

| Mode | Root |
|---|---|
| Single mode | `<data path>/prompt-cache` |
| Distributed worker | `<worker data dir>/prompt-cache`, on the local disk of the worker |

Under the root, the backend writes one directory per model: `llama-cpp/<build>-<model>/`, mode `0700`. The `<build>` part changes with the llama.cpp build, so an update starts a new directory and the old one expires. The directory also holds a `manifest.json` that describes the model and the cache format, and two small files, `.lock` and `.speed`.

## Sizes

The KV state is large:

- A dense 8B model with an `f16` KV cache uses about 128 KiB per token. An entry of 8,000 tokens is about 1 GiB.
- A hybrid model adds about 63 MiB per recurrent checkpoint. An entry keeps at most 3 checkpoints.
- A quantized KV cache (`cache_type_k`, `cache_type_v`) makes the entries smaller.

## When an entry is written and read

The backend writes an entry only when all these are true:

- the prompt has at least 1,024 tokens (`prompt_cache_disk_min_tokens`). A shorter prompt is faster to compute again;
- the start of the prompt was seen in at least two requests: a conversation with several turns, or a system prompt or tool list that requests share. A one-shot prompt is never written;
- the request may keep content (see [Incognito](#incognito-and-zero-retention-requests));
- the disk has room (see [Low disk](#low-disk)).

The backend reads an entry only when reading it is faster than computing it again. At start, LocalAI measures how fast the cache disk delivers data that is not in memory (64 MiB, read once per root, in the background). The backend compares the time to transfer the entry with the time to compute its tokens. A backend that starts before the measurement ends gets no speed and decides without it.

## Isolation per user

Each user has a separate cache. LocalAI derives a namespace from the account that sends the request:

| Request | Namespace |
|---|---|
| Web session or user API key | the user |
| Key of a paired device (client key) | that device |
| Legacy API key from the environment | one namespace for all such keys |
| Authentication off | one local namespace |

LocalAI sends the backend a salt that it derives from the namespace. The salt is part of the key of every entry and of its file name. A request never reads an entry of another namespace. The namespace comes only from the authenticated account, never from request fields.

### Shared cache

With `prompt_cache_disk_shared` on (the server setting, or the model option), all users read and write one cache. A long system prompt is then computed once for everybody. Turn it on only when every user may see the prompts of the other users: an entry holds the prompt text.

## Incognito and zero-retention requests

A request of a zero-retention [incognito chat]({{%relref "/features/chats-and-agents/incognito" %}}) is never written to disk. The backend keeps its state in RAM only, under a private key, and drops it when the request ends.

## Turning it off

- For the whole server: set **Keep long prompts on disk** off in Settings > Models in memory, or start with `--prompt-cache-disk=false` (`LOCALAI_PROMPT_CACHE_DISK=false`). Backends get the change when they next start.
- For one model: add `prompt_cache_disk:false` to its `options:`.

```yaml
options:
  - prompt_cache_disk:false
```

- For one worker: start the worker with `LOCALAI_PROMPT_CACHE_DISK=false`.

## Settings

The server settings are in Settings > Models in memory > Disk prompt cache, and in `runtime_settings.json`. A model option of the same name wins for that model.

| Setting | Model option | Default | Meaning |
|---|---|---|---|
| `prompt_cache_disk` | `prompt_cache_disk` | `true` | Keep long prompts on disk. |
| `prompt_cache_disk_ttl` | `prompt_cache_disk_ttl_hours` | `168h` (7 days) | Time to keep an entry after its last use. |
| `prompt_cache_disk_max_size_mib` | `prompt_cache_disk_max_size_mib` | `0`: automatic | Size cap. The server cap is for the whole node; the model option is for one model. |
| `prompt_cache_disk_user_quota_mib` | `prompt_cache_disk_quota_mib` | `0`: automatic | Most disk one user may use. |
| `prompt_cache_disk_min_tokens` | `prompt_cache_disk_min_tokens` | `1024` | Shortest prompt that is written. |
| `prompt_cache_disk_shared` | `prompt_cache_disk_shared` | `false` | One cache for all users. |
| `prompt_cache_disk_low_free_mib` | `prompt_cache_disk_low_free_mib` | `0`: max(15% of the disk, 10 GiB) | Low free-space mark. |
| `prompt_cache_disk_critical_free_mib` | `prompt_cache_disk_critical_free_mib` | `0`: 5% of the disk | Critical free-space mark. Never above the low mark. |

The automatic size cap is the smaller of 20 GiB and a tenth of the free disk, measured at start. The automatic quota is an equal share of the cap when authentication is on and has more than one user; otherwise there is no quota. Set the quota to `-1` for no quota.

The defaults need no tuning. Change the marks only for a small disk or a test machine.

## Low disk

The cache never fills the disk. Every few minutes, and before each write, the cache checks the free space of its disk:

| Free space | State | What happens |
|---|---|---|
| Above the low mark | On | The cache works. It may grow up to its cap, and never past the free space over the low mark. |
| Under the low mark: max(15% of the disk, 10 GiB) | Paused, low disk | Nothing new is written. The oldest entries are deleted until the disk has the low mark free again, or the cache is empty. Not more is deleted than needed. |
| Under the critical mark: 5% of the disk | Disabled, disk almost full | Nothing is written. The oldest entries are deleted as in the paused state. The server logs one warning. The cache comes back only when the free space is above the low mark again. |

When the cache and the models are on the same disk, the cache uses the numbers of that disk. So the cache never takes the room that a model download needs.

Settings > Models in memory shows the size of the cache, the free disk, the cap in effect and the state, with a message when the cache is paused or disabled.

## Retention

What is kept: the entries described in [What is stored](#what-is-stored), on the disk of the node that ran the model.

How long:

- An entry is deleted 7 days after its last use (`prompt_cache_disk_ttl`, or `prompt_cache_disk_ttl_hours` of the model). A read counts as a use.
- The size cap, the quota of each user and the low-disk rules delete the least recently used entries first.
- A partial write older than 1 hour (after a crash) is deleted.

A cleaner on each node applies these rules at start and every 5 minutes. It deletes only the files of the cache layout.

How to clear it:

| Who | How | What goes |
|---|---|---|
| Administrator | Settings > Models in memory > **Clear**, or `POST /api/prompt-cache/clear` | All entries of all users and models |
| Administrator | `POST /api/prompt-cache/clear` with `{"model": "<name>"}` | All entries of one model |
| A user | Account > Cached prompts > **Clear my cached prompts**, or `DELETE /api/prompt-cache/mine` | The entries of the user and of the user's paired devices |
| Administrator | Delete a user | The entries of the user and of the user's paired devices |
| A user or an administrator | Revoke a paired device (client key) | The entries of that device |

**Clear my cached prompts** and the deletion of a user or a device do not delete the entries of the shared cache: they belong to no user. An administrator clear deletes them.

Deleting a chat does not delete its entries: the cache does not record which chat an entry came from. The entries expire with the time to live. To delete them at once, use **Clear my cached prompts**.

The administrator view (`GET /api/prompt-cache`) shows, for each node, the size, the number of entries and of namespaces, the state of the disk and the size of each model. It does not show prompt text or user names. The LocalAI Assistant has the same view and the clear as the `get_prompt_cache` and `clear_prompt_cache` tools.

## Distributed mode

Each worker keeps its cache on its own local disk and runs its own cleaner, with the default settings and the `LOCALAI_PROMPT_CACHE_DISK` switch of the worker. In this version:

- the server settings of the frontend do not reach the workers;
- a clear, and the purge when a user or a device is deleted, act on the files of the node that answers the request (the frontend). The files on the workers expire with their time to live;
- the usage view shows the frontend node only.

With several frontends, each frontend has its own cache root and its own cleaner: the state is per instance. A cache that several nodes share (for example on network storage) is planned, not available now.

## API

| Method and path | Who | Body | Answer |
|---|---|---|---|
| `GET /api/prompt-cache` | Administrator | | `{enabled, shared, nodes: [{node, bytes, files, namespaces, status, models}]}`. `status.state` is `on`, `paused_low_disk`, `disabled_disk_full` or `unknown`. |
| `POST /api/prompt-cache/clear` | Administrator | `{"model": "<name>"}`, or empty for all | `{files, bytes}` |
| `DELETE /api/prompt-cache/mine` | Any user | | `{files, bytes}` |
