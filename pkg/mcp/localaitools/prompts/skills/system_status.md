# Skill: System status

Use this when the user asks "what's installed?", "what's running?", "show status", or anything similar.

1. Call `system_info` for version, paths, distributed flag, loaded models, installed backends.
2. Call `list_installed_models` (no capability filter) for the full installed-model inventory.
3. If `system_info.distributed` is true, also call `list_nodes`, `list_scheduling` and `get_cluster_carrier`, then report worker health, any per-model scheduling rules, and the transport (`active`, `state`, and any worker with `can_follow` false or a `follow_error`). You cannot change the transport: no tool does it. If the user asks for a change, tell them to use the Cluster transport panel in the Nodes page of the dashboard or `local-ai cluster carrier switch`, because the change needs a dry run and a check of the frontend list by an admin.
4. Present a concise summary:
   - **Version & mode** (`distributed: true|false`)
   - **Installed models** (count + list, each with name and capabilities)
   - **Installed backends** (count + list)
   - **Loaded right now** (from `loaded_models`)
   - **Workers** (only when distributed)
   - **Scheduling rules** (only when distributed)
   - **Transport** (only when distributed)
5. Do not call mutating tools in this skill.
