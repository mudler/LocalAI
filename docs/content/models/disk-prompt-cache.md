+++
disableToc = false
title = "Disk Prompt Cache"
weight = 101
description = "Keep the prompt cache of long llama.cpp prompts on disk, per user, so a model reload does not compute them again."
keywords = ["prompt cache","kv cache","llama.cpp","disk","retention","privacy"]
+++

The `llama-cpp` backend keeps the KV state of earlier prompts in RAM (the [server-side prompt cache]({{%relref "/models/capabilities/text/text-generation#server-side-prompt-cache-repeated-system-prompts" %}})). That cache is lost when the model unloads. The disk prompt cache keeps the state of long prompts on disk too. After a reload, a swap or a restart, a request with the same start reads the state from disk and does not compute it again.

The disk prompt cache is on by default. It needs a directory on a local disk: `--prompt-cache-path` (`LOCALAI_PROMPT_CACHE_PATH`), or the data path (`--data-path`, `LOCALAI_DATA_PATH`). With `LOCALAI_DATA_PATH=-` and no `--prompt-cache-path`, it is off.

## What is stored

Each entry holds:

- the prompt tokens, in clear. They can be read back as text;
- the KV state of the model for those tokens;
- for a hybrid model (Qwen3.5, LFM2, Granite-H), up to 3 checkpoints of the recurrent state.

Treat these files like stored chats. They contain what the users wrote, and the system prompts and tool results of their chats.

## Where it is stored

| Mode | Root |
|---|---|
| Single mode | `--prompt-cache-path`, else `<data path>/prompt-cache` |
| Distributed worker | `--prompt-cache-path`, else `<data path>/prompt-cache`, else `<models path>/../data/prompt-cache` when it is on a local disk. See [Worker setup](#worker-setup). |

The root must be on a local disk that the node can write. At start, each node checks its root:

- On a network filesystem (NFS, SMB/CIFS, a remote FUSE filesystem such as sshfs, s3fs or rclone, Ceph, 9p, AFS and similar), the cache is off on that node. A FUSE mount of a local disk (virtiofs, fuseblk, the file sharing of Docker Desktop) counts as local.
- When the node cannot create the root or write a file in it, the cache is off on that node.

In both cases the node logs one warning with the path, and Settings shows the reason for that node, for example "Off: network disk". To fix it, set `--prompt-cache-path` or `LOCALAI_PROMPT_CACHE_PATH` to a directory on a local disk. On Windows and on platforms other than Linux and macOS, the type of the filesystem is not known, and only the write check applies.

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

- For one worker: start the worker with `LOCALAI_PROMPT_CACHE_DISK=false`. This only turns the tier off on that worker; the other settings of a worker come from the cluster (see [Distributed mode](#distributed-mode)).

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

In distributed mode each worker keeps its own disk prompt cache. The frontends keep the settings and the clears once, for the whole cluster, and bring every worker up to date over the node control channel (the control verbs `promptcache.sync` and `promptcache.usage`, on NATS or on the tunnel of the worker).

### Worker setup

A worker chooses the root of its cache in this order:

1. `--prompt-cache-path` (`LOCALAI_PROMPT_CACHE_PATH`).
2. `<data path>/prompt-cache`, with `--data-path` (`LOCALAI_DATA_PATH`).
3. `<models path>/../data/prompt-cache`, but only when that directory is on a local disk.

Step 3 keeps older workers working. Do not rely on it in a container. The models often sit on a network share at `/models`. Then `/data` is on the ephemeral root filesystem of the container, and the cache is lost when the container is replaced. Give the worker a data path on a local volume instead.

A worker without a usable root runs without the disk cache, logs one warning, and Settings shows it as off with the reason.

With `docker run`, mount a local directory at the data path:

```bash
docker run -d --name localai-worker   -v /srv/localai/data:/data   -v /mnt/models:/models   -e LOCALAI_DATA_PATH=/data   -e LOCALAI_MODELS_PATH=/models   -e LOCALAI_REGISTER_TO=http://frontend:8080   -e LOCALAI_REGISTRATION_TOKEN=<token>   localai/localai:latest-gpu-nvidia-cuda-12 worker
```

With docker compose, do the same with a named volume:

```yaml
services:
  worker:
    image: localai/localai:latest-gpu-nvidia-cuda-12
    command: worker
    environment:
      LOCALAI_DATA_PATH: /data
      LOCALAI_MODELS_PATH: /models
      LOCALAI_REGISTER_TO: http://frontend:8080
      LOCALAI_REGISTRATION_TOKEN: <token>
    volumes:
      - worker-data:/data
      - /mnt/models:/models

volumes:
  worker-data:
```

On Kubernetes, mount an `emptyDir` with a `sizeLimit` at the data path. The cache then lives as long as the pod. For a cache that survives a new pod on the same node, use a PVC of a local-path storage class instead.

```yaml
spec:
  containers:
    - name: worker
      image: localai/localai:latest-gpu-nvidia-cuda-12
      args: ["worker"]
      env:
        - name: LOCALAI_DATA_PATH
          value: /data
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      emptyDir:
        sizeLimit: 50Gi
      # Or a local-path PVC:
      # persistentVolumeClaim:
      #   claimName: localai-worker-data
```

Keep the `sizeLimit` above the cache cap and the low free-space mark. The cache measures the free space of the volume, and the kubelet evicts a pod that goes over the limit.

### Who owns what

| What | Owner | Where |
|---|---|---|
| Files | The worker that runs the backend | The root of the worker (see [Worker setup](#worker-setup)), on the local disk of the worker. Its backends write the files and its own cleaner deletes them. A frontend never reads or writes them. No shared storage is needed or used. |
| Settings | The cluster | Stored once in the database with the other cluster settings (the memory settings of Settings > Models in memory). Every frontend applies the same values. The workers get them over the control channel, not from their own environment. |
| Clears and purges | The cluster | Stored in a purge log in the database. The frontend that is asked sends them to every worker at once. |
| Usage | Each worker | Each worker reports its own size, free disk, cap in effect and state. The frontend asks every worker and adds up the total. |

The model option `prompt_cache_disk:false` and the other model options travel with the model when it loads, as in single mode.

### Settings

A change of a setting reaches every worker within seconds: the frontends store it in the database, and one frontend sends it to each worker. A worker gets the settings again when it restarts (the frontends see its new process) and at least every 30 seconds while it is behind. A worker that has not received the settings yet, for example just after it starts, runs the defaults.

The automatic per-user quota is a share of the cap of each worker: the frontends send the number of users, and each worker computes its quota from its own cap.

`LOCALAI_PROMPT_CACHE_DISK=false` on a worker turns the tier off on that worker only. It can only turn the tier off: the cluster setting cannot turn it on against it.

### Clears and offline workers

A clear (all, one model, **Clear my cached prompts**, the deletion of a user or a device) is written to the purge log first, then sent to every worker that is online. The clear succeeds once it is in the log. A worker that is offline, or that does not answer, applies the clears it missed when it is back: one frontend at a time checks every 30 seconds which workers are behind and sends them what they missed, oldest first.

- A clear of all entries or of one model deletes only the entries last used before the clear was asked for. A clear that arrives late, or twice (two frontends may send it at once), never deletes entries written after it.
- A clear of a user or a device deletes every entry of that namespace, whenever it arrives.
- The purge log keeps a clear for 30 days. A worker that was away longer than that, and missed a clear that the log dropped, deletes all entries last used before that clear when it is back. So a worker never keeps an entry that a clear should have deleted.
- The database records, for each worker, the last clear it applied and the settings it runs. The record of a worker that leaves the cluster is deleted.

Deleting a chat does not delete its entries, as in single mode.

### Several frontends

- Settings: one copy in the database, shared by every frontend (shared state, see the cluster settings).
- Purge log and the record of each worker: one copy in the database. Any frontend can record a clear and send it.
- The pass that brings workers up to date runs on one frontend at a time (an advisory lock). A worker skips a clear that it already applied, so a clear that two frontends send at once is applied once.
- Each frontend may also have a cache root under its own data path. It holds nothing in distributed mode: the backends run on the workers.

### Usage

`GET /api/prompt-cache` and Settings > Models in memory list every worker: its size, entries, free disk, cap in effect and state (on, paused, disabled, or off on that worker). A worker that is off says why in `off_reason`: `no_path` (no root), `switched_off` (the setting or the switch of the worker), `network_disk` or `not_writable`. A worker that is offline or does not answer is listed with the reason. The `total` adds up the workers that answered.

A cache that several workers share (for example on network storage), and routing a request to the worker that has its prompt on disk, are planned, not available now.

## Measured

One GPU host with a local NVMe disk, llama.cpp, an 8B dense model at 4-bit and a 35B-A3B hybrid model at 8-bit. Two turns share a prompt, the model unloads, a new process loads it, and a third turn shares the prompt.

| Model | Prompt | First token, cold | First token after a reload |
|---|---|---|---|
| 35B-A3B hybrid | 8k tokens | 5.1 s | 0.4 s |
| 35B-A3B hybrid | 32k tokens | 19.2 s | 0.8 s |
| 8B dense | 8k tokens | 2.5 s | 0.9 s |
| 8B dense | 32k tokens | 14.3 s | 3.3 s |

- Entries are written in the background while the server is idle, so an unload rarely has much left to save. An unload right after the last request (a model swap) still found the 32k-token entry of the earlier turn on disk.
- Decoding streams do not slow down: with ten 1 GiB entries written during a decode stream, its largest gap and its 99th-percentile gap were no larger than with the tier off.
- On a network share at 117 MB/s, the read-speed gate wrote nothing: reading the entry would be slower than computing the prompt on that GPU.
- An entry of a dense model with a long prompt is large: about 4.5 GiB for 32k tokens of an 8B model with f16 KV. A KV cache at q8_0 halves it.
- Two requests that branch off the same long prompt (two answers to one question, a regenerated reply) keep two files, each with the whole shared prompt. A multi-turn chat keeps one: each turn's file replaces the one before. The per-user quota, the cap and the TTL bound the total ([issue #13543](https://github.com/mudler/LocalAI/issues/13543) tracks keeping one file).

## API

| Method and path | Who | Body | Answer |
|---|---|---|---|
| `GET /api/prompt-cache` | Administrator | | `{enabled, shared, nodes: [{node, enabled, off_reason, error, bytes, files, namespaces, status, models}], total: {bytes, files, cap_bytes, nodes, unreachable}}`. `status.state` is `on`, `paused_low_disk`, `disabled_disk_full` or `unknown`. `error` is set for a node that gave no usage. |
| `POST /api/prompt-cache/clear` | Administrator | `{"model": "<name>"}`, or empty for all | `{files, bytes}` |
| `DELETE /api/prompt-cache/mine` | Any user | | `{files, bytes}` |
