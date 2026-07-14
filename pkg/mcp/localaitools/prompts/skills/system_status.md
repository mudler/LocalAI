# Skill: System status

Use this when the user asks "what's installed?", "what's running?", "show status", or anything similar.

1. Call `system_info` for version, paths, distributed flag, loaded models, installed backends.
2. Call `list_installed_models` (no capability filter) for the full installed-model inventory.
3. If `system_info.distributed` is true, also call `list_nodes` and report worker health.
4. Present a concise summary:
   - **Version & mode** (`distributed: true|false`)
   - **Installed models** (count + list, each with name and capabilities)
   - **Installed backends** (count + list)
   - **Loaded right now** (from `loaded_models`)
   - **Workers** (only when distributed)
5. Do not call mutating tools in this skill.
