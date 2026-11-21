---
description: Report what is installed and running: version, models, backends, workers.
---
# Skill: System status

Use this when the user asks "what's installed?", "what's running?", "show status", or anything similar.

1. Call `system_info` for version, paths, distributed flag, loaded models, installed backends.
2. Call `list_installed_models` (no capability filter) for the full installed-model inventory.
3. If `system_info.distributed` is true, also call `list_nodes`, `list_scheduling` and `get_cluster_carrier`, then report worker health, any per-model scheduling rules, and the broadcast bus between the frontends (`active`, `state`, and any worker with `can_follow` false or a `follow_error`). `active` is `nats` (NATS) or `tunnel` (PostgreSQL as the bus, with workers attached over WebSocket); a worker may also attach over P2P. Workers never connect to PostgreSQL: only the frontends use it. You cannot change the bus: no tool does it. If the user asks for a change, tell them it is an advanced operation of the admin API or `local-ai cluster carrier switch`, because the change needs a dry run and a check of the frontend list by an admin. The Nodes page shows the bus but does not change it.
4. Present a concise summary:
   - **Version & mode** (`distributed: true|false`)
   - **Installed models** (count + list, each with name and capabilities)
   - **Installed backends** (count + list)
   - **Loaded right now** (from `loaded_models`)
   - **Workers** (only when distributed)
   - **Scheduling rules** (only when distributed)
   - **Transport** (only when distributed)
5. Do not call mutating tools in this skill.
