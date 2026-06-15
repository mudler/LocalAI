# Tool catalog

The MCP `tools/list` endpoint also exposes the full input schema for each of these. The list below is the canonical curated description.

## Read-only

- `gallery_search` — Search configured galleries for installable models.
- `list_installed_models` — List models currently installed on this LocalAI. Optional `capability` filter (e.g. `chat`, `embed`, `image`).
- `list_galleries` — List configured model galleries.
- `list_backends` — List installed backends.
- `list_known_backends` — List backends available to install from configured backend galleries.
- `get_job_status` — Poll the status of an install/delete/upgrade job by id.
- `get_model_config` — Read the YAML/JSON config of an installed model.
- `vram_estimate` — Estimate VRAM use for a model under a given config.
- `system_info` — LocalAI version, paths, distributed flag, loaded models, installed backends.
- `list_nodes` — List federated worker nodes (only useful in distributed mode).

## Mutating (require user confirmation per safety rule 1)

- `install_model` — Install a model from a gallery. Returns a job id; poll with `get_job_status`.
- `import_model_uri` — Install a model from an arbitrary URI (HuggingFace, OCI, http(s), file://). May return `ambiguous_backend` when several backends apply; call again with `backend_preference` to disambiguate.
- `delete_model` — Delete an installed model.
- `install_backend` — Install a backend.
- `upgrade_backend` — Upgrade an installed backend by name.
- `edit_model_config` — Patch (deep-merge) JSON into an installed model's config.
- `reload_models` — Reload all model configs from disk.
- `toggle_model_state` — Enable or disable a model (`action`: `enable` or `disable`).
- `toggle_model_pinned` — Pin or unpin a model (`action`: `pin` or `unpin`).
