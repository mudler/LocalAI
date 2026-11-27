# Prompt cache on disk: the backend contract

LocalAI keeps a backend's prompt (prefix KV) cache on the local disk of each
node, so a model reload starts warm. LocalAI owns the policy, the settings,
the retention and the purges. A backend owns only the mechanics: how it saves
and loads its own state. This file says what a backend must do to plug in.
The llama.cpp backend is the reference implementation.

## What LocalAI does for every backend

- **Namespaces.** It derives a salt from the authenticated principal and sends
  it with each request: `PredictOptions.PromptCacheSalt`, 64 lowercase hex
  characters, or empty for the shared namespace. It is never taken from
  request Metadata (`core/promptcache`).
- **Zero retention.** It sets `PredictOptions.NoPromptCache` for zero-retention
  and incognito requests.
- **A directory and the policy.** The node that starts the backend process
  (the frontend in single mode, the worker in distributed mode) sets the
  environment below. No `LOCALAI_PROMPT_CACHE_ROOT` means the tier is off on
  this node: do nothing.
- **Retention.** A janitor on each node removes expired files, applies the
  per-user quota, the node cap and the free-space watermarks, and runs the
  purges (all, one model, one user). It reads only file names and
  modification times, so it needs no backend code (`pkg/promptcache`).
- **Settings and purges in a cluster.** The frontends store them once and send
  them to every worker; each worker passes them to its backends as the
  environment below.

## Environment

| Variable | Meaning | Default when unset |
|---|---|---|
| `LOCALAI_PROMPT_CACHE_ROOT` | Root directory of the tier on this node. Unset: tier off. | off |
| `LOCALAI_PROMPT_CACHE_READ_MBPS` | Cold read speed of the root in MB/s, measured once by the node | unknown |
| `LOCALAI_PROMPT_CACHE_TTL_HOURS` | Delete and ignore entries unused for longer | 168 |
| `LOCALAI_PROMPT_CACHE_MIN_TOKENS` | Do not write shorter prompts | 1024 |
| `LOCALAI_PROMPT_CACHE_MIN_REUSE` | Write a prefix only after this many requests shared it | 2 |
| `LOCALAI_PROMPT_CACHE_QUOTA_MIB` | Most one namespace may keep in your directory, 0 = none | 0 |
| `LOCALAI_PROMPT_CACHE_MAX_SIZE_MIB` | Cap of your model directory, 0 = none | 0 |
| `LOCALAI_PROMPT_CACHE_LOW_FREE_MIB` | Low free-space watermark, 0 = max(15% of the filesystem, 10 GiB) | 0 |
| `LOCALAI_PROMPT_CACHE_CRITICAL_FREE_MIB` | Critical free-space watermark, 0 = 5% | 0 |
| `LOCALAI_PROMPT_CACHE_MAX_CHECKPOINTS` | Most recurrent checkpoints per entry (hybrid models) | 3 |
| `LOCALAI_PROMPT_CACHE_LOAD_OVERHEAD_MS` | Fixed cost of one load in the read-or-recompute gate | 50 |

Use these numbers. Do not hard-code your own. A backend may let model options
override them for one model, as llama.cpp does.

## What a backend must do

1. **Isolate namespaces.** Two prompts with different salts must never share a
   cached prefix: not on disk, not in RAM, not between live sequences. The
   simplest way is to put the salt in the prefix key, for example as the seed
   of a prefix hash chain.
2. **Honour zero retention.** For a request with `NoPromptCache`, use a
   private salt: the request's salt with every byte XORed with `0x5a`. Never
   write a prompt that holds tokens of such a request, and never read the disk
   for it. Keeping it in RAM for the life of the process is allowed.
3. **Lay out files so the janitor can work:**
   - Your model directory: `<root>/<backend>/<pin16>-<model16>/`, mode 0700.
     `<backend>` is your backend name (`llama-cpp`). `<pin16>` is 16 hex
     characters that change when your state format can change (engine commit,
     patches). `<model16>` is the first 16 hex characters of the SHA-256 of
     `ModelOptions.Model`; LocalAI computes the same value to purge one model.
   - An entry: `<compat16>-<ns16>-<hash32>.cache`. `<ns16>` is the first 16
     hex characters of the salt (16 zeros for the shared namespace).
     `<compat16>` changes with everything that makes the state unusable
     (model file, KV types and layout, RoPE). `<hash32>` identifies the prefix.
   - Write to `<name>.tmp`, then rename. Never leave a partial file under a
     final name.
   - Set the modification time of an entry to now when you load it: the
     janitor's TTL and LRU read it.
   - Write `manifest.json` in your model directory: what the entries depend on
     (format, engine pin, state version, model identity, model file size and
     time, KV types, compat id). A tier shared between nodes will read it.
   - Write `.speed` in your model directory: one line `prefill_tps <value>`,
     the measured prompt speed of the model, so the gate knows it at the next
     start.
   - Expect files to disappear at any time (janitor, purges, other processes):
     scan the directory again before you use it.
4. **Gate reads and writes.** Read an entry only when
   `bytes / (READ_MBPS * 1e6) + LOAD_OVERHEAD_MS / 1000` is less than the time
   to compute the tokens it adds at the measured prompt speed. Write only what
   could pass that test later, only prompts of at least `MIN_TOKENS`, and only
   prefixes seen in at least `MIN_REUSE` requests. Unknown speeds do not
   block.
5. **Watch free space before each write.** Below the low watermark, do not
   write and remove your least recently used entries until the watermark is
   met. Below the critical watermark, stop writing until free space is above
   the low watermark again. The janitor does the same every few minutes; the
   backend check keeps a burst of writes from filling the disk in between.
6. **Never stall a stream.** Write from a thread of your own with a bounded
   queue that drops its oldest entry when full. A lookup must not wait for a
   write.
7. **Save before unload, in bounded time.** Implement `Free()`: save what is
   worth keeping, most recently used first, for at most 3 s (LocalAI waits 5 s).
   If your process cannot load another model afterwards, answer
   `Unimplemented`: LocalAI then stops the process as before. A stop without
   `Free()` (SIGTERM) must be bounded the same way.

## Reference implementation (llama.cpp)

| Part | Where |
|---|---|
| Policy numbers and decisions, unit tested | `backend/cpp/llama-cpp/prompt_cache_policy.h`, `prompt_cache_policy_test.cpp` |
| Disk tier (upstream #32303, backported) | `backend/cpp/llama-cpp/patches/0006-server-prompt-cache-disk.patch` |
| Namespaces, zero retention, writer thread, gates, retention, watermarks | `backend/cpp/llama-cpp/patches/0007-prompt-cache-disk-localai.patch`, hooks D1 to D8 in `PATCHES.md` |
| Environment, options, `manifest.json`, `Free()` | `backend/cpp/llama-cpp/grpc-server.cpp` (`apply_prompt_cache_key`, `Free`, the `prompt_cache_disk` options) |
| End-to-end test (reload warm, other user, zero retention) | `tests/e2e-backends/prompt_cache_disk_test.go` |

A new backend can reuse `prompt_cache_policy.h` as it is (no engine types),
and the e2e spec with `BACKEND_BINARY` pointing at its `run.sh`.

## Other LocalAI backends

| Backend | State | Cost to adopt |
|---|---|---|
| `turboquant`, `bonsai`, `olala` (llama.cpp forks) | They build the same `grpc-server.cpp` and `prepare.sh`, so the request handling, `Free()` and the policy header are already in. Their Makefiles drop `patches/` (the fork trees differ), so 0006 and 0007 are not applied: `LOCALAI_HAS_PROMPT_CACHE_DISK` is 0 and the tier stays off. | Small to medium per fork: port 0006 and 0007 into the fork's own `patches/`. Feasible when the fork's server keeps upstream's prefix hash chain (#32154) and `server-slot-sched.*`. |
| `ik-llama-cpp` | Its own `grpc-server.cpp` and server; no disk tier. | Medium: the fork's server predates the upstream prompt cache layout, so 0006 does not apply; follow this contract with its slot save/restore API. |
| vLLM, SGLang | Python engines with their own prefix caches. | Separate effort: an LMCache-style connector that maps the salt to the cache key and writes under the layout above. |
| Other backends | No prompt cache. | Not applicable. |
