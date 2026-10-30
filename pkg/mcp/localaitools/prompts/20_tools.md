# Tool catalog

The MCP `tools/list` endpoint also exposes the full input schema for each of these. The list below is the canonical curated description.

## Read-only

- `gallery_search` — Search configured galleries for installable models.
- `list_installed_models` — List models currently installed on this LocalAI. Optional `capability` filter (e.g. `chat`, `embed`, `image`).
- `list_galleries` — List configured model galleries.
- `list_backends` — List installed backends.
- `list_backend_library_updates` — List installed library backends that have a newer build in their library. Read-only. To take one, confirm, then call `install_library_backend` with the listed `sourceId`, `name` and `version`.
- `list_known_backends` — List backends available to install from configured backend galleries.
- `get_job_status` — Poll the status of an install/delete/upgrade job by id.
- `get_model_config` — Read the YAML/JSON config of an installed model.
- `vram_estimate` — Estimate VRAM use for a model under a given config.
- `system_info` — LocalAI version, paths, distributed flag, loaded models, installed backends.
- `list_nodes` — List federated worker nodes (only useful in distributed mode).
- `list_scheduling` — List distributed per-model scheduling configs.
- `get_scheduling` — Read the distributed scheduling config for one model.
- `list_voice_profiles` — List reusable voice-cloning profiles and their stable TTS voice URIs.
- `list_shared_folders` — List the shared folders with their id, page count and members (a user or `@everyone`, mode `read` or `write`).
- `list_shared_folder_events` — Read the audit of the shared folders, newest first.
- `list_chat_tools` — List the built-in chat tools (bash on this machine, the web tools): on or off, who serves each, and the host-commands mode.
- `list_host_exec_events` — Read the audit of the commands the chats' built-in bash ran directly on this machine, newest first.
- `get_branding` — Read the resolved instance name, tagline, and branding asset URLs.
- `get_usage_stats` — Read token/request usage aggregates when usage tracking is enabled.
- `get_pii_events` — Inspect recent PII-filter events without returning redacted request bodies.
- `get_middleware_status` — Inspect middleware configuration and health.
- `get_router_decisions` — Inspect recent router decisions and classifier signals.
- `get_router_corpus_stats` — Inspect a KNN router corpus by count and label only; exemplar texts are never returned.
- `list_aliases` — List configured model aliases and their targets.
- `list_failover_chains` — List failover chains, their active target and target health.

## Mutating (require user confirmation per safety rule 1)

- `install_model` — Install a model from a gallery. Returns a job id; poll with `get_job_status`.
- `import_model_uri` — Install a model from an arbitrary URI (HuggingFace, OCI, http(s), file://). May return `ambiguous_backend` when several backends apply; call again with `backend_preference` to disambiguate.
- `delete_model` — Delete an installed model.
- `install_backend` — Install a backend. Fails with "backend installation is disabled" on servers that manage backends outside LocalAI; report that rather than retrying.
- `install_library_backend` — Install a backend from an add-on library entry by `source_id`, `name` and `version`. The call returns when the install is done, with no job id. The backend gallery is not changed.
- `upgrade_backend` — Upgrade an installed backend by name. Same refusal as `install_backend` on such servers.
- `edit_model_config` — Patch (deep-merge) JSON into an installed model's config.
- `reload_models` — Reload all model configs from disk.
- `cancel_model_load` — Cancel one distributed load by the exact job_id from load-status. `stopping` means pending, not proof the work stopped.
- `load_model` — Pre-load a model into memory so the first request pays no cold-start cost. For a realtime pipeline model, every sub-model (VAD, transcription, LLM, TTS, sound_detection, voice_recognition) is loaded. Inverse of stopping a model.
- `toggle_model_state` — Enable or disable a model (`action`: `enable` or `disable`).
- `toggle_model_pinned` — Pin or unpin a model (`action`: `pin` or `unpin`).
- `set_branding` — Change the instance name or tagline.
- `set_alias` — Create, update, or remove a model alias.
- `seed_router_corpus` — Add validated labelled exemplars to a KNN router corpus.
- `clear_router_corpus` — Permanently clear a KNN router corpus and its live index entries.
- `create_voice_profile` — Save a consent-confirmed base64 PCM-WAV reference and exact transcript for reuse in TTS.
- `delete_voice_profile` — Permanently delete a saved voice profile by UUID.
- `create_shared_folder` — Create the shared folder `/shared/<name>` with no members.
- `rename_shared_folder` — Rename a shared folder by id; its members and pages stay.
- `delete_shared_folder` — Move a shared folder to the trash. Returns `undo_rev`; drops every user's consent to its pages.
- `restore_shared_folder` — Restore a removed shared folder with its `undo_rev`.
- `set_shared_folder_member` — Give a user (ID, email or name) or `@everyone` `read` or `write` access to a shared folder, or change it.
- `remove_shared_folder_member` — Take a member's access to a shared folder away.
- `set_node_vram_budget` — Set or clear a federated node's VRAM budget override.
- `set_scheduling` — Create or update a distributed per-model scheduling config.
- `delete_scheduling` — Remove a distributed per-model scheduling config.
- `pin_failover_target` — Force a failover chain to one target.
- `unpin_failover_target` — Remove a failover pin.
