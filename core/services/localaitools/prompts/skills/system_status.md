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
5. When the user asks about the disk prompt cache (its size, or why it is paused or off), call `get_prompt_cache`. `paused_low_disk`: the disk has less free space than the low mark, so the cache writes nothing new and shrinks. `disabled_disk_full`: the disk is almost full, so the cache is off until the free space is above the low mark again. To clear the cache of one model (`model`) or of all models, use `clear_prompt_cache` only after the confirmation of safety rule 1. A clear acts on the node that answers; users clear their own cached prompts from their account page.
6. Do not call mutating tools in this skill without that confirmation.
