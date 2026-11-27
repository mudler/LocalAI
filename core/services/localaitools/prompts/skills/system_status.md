---
description: Report what is installed and running: version, models, backends, workers.
---
# Skill: System status

Use this when the user asks "what's installed?", "what's running?", "show status", or anything similar.

1. Call `system_info` for version, paths, distributed flag, loaded models, installed backends.
2. Call `list_installed_models` (no capability filter) for the full installed-model inventory.
3. If `system_info.distributed` is true, also call `list_nodes` and `list_scheduling`, then report worker health, the routes of each worker (`routes`: NATS, or a tunnel over WebSocket or P2P, and which of them are live), and any per-model scheduling rules. Each worker holds the routes its operator gave it. The frontends broadcast over NATS while they reach it, else over PostgreSQL. Workers never connect to PostgreSQL.
4. Present a concise summary:
   - **Version & mode** (`distributed: true|false`)
   - **Installed models** (count + list, each with name and capabilities)
   - **Installed backends** (count + list)
   - **Loaded right now** (from `loaded_models`)
   - **Workers** (only when distributed)
   - **Scheduling rules** (only when distributed)
   - **Routes** (only when distributed)
5. When the user asks about the disk prompt cache (its size, or why it is paused or off), call `get_prompt_cache`. `paused_low_disk`: the disk has less free space than the low mark, so the cache writes nothing new and shrinks. `disabled_disk_full`: the disk is almost full, so the cache is off until the free space is above the low mark again. A node that is off has `off_reason`: `network_disk` or `not_writable` means its cache directory is on a network disk or cannot be written, and the fix is `--prompt-cache-path` (`LOCALAI_PROMPT_CACHE_PATH`) set to a directory on a local disk of that node; `no_path` means the node has no data path. To clear the cache of one model (`model`) or of all models, use `clear_prompt_cache` only after the confirmation of safety rule 1. In distributed mode the answer lists every worker and the total; a clear reaches every worker, and a worker that is offline applies it when it is back. Users clear their own cached prompts from their account page.
6. Do not call mutating tools in this skill without that confirmation.
