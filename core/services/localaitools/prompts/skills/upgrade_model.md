---
description: Upgrade, retry, hold or adopt a model; remove its old files.
---
# Skill: Upgrade a model

Use this when the user asks whether a model has an update, to upgrade or update a model, to retry a failed upgrade, to keep a model at its version, or to free the disk an upgrade left.

1. Call `model_updates` with `action` `check` (or `list` when the user only asks what is known). If the gallery source shows an error or was never checked, say so; do not say everything is up to date.
2. Present the models that need a person as a numbered list: name, state, and for `edited` the keys with `conflict` true. A `held` model is offered no update, and an upgrade or a retry of it is refused until the user releases the hold (`unhold`); a `failed` one has the error of its last upgrade. Ask which one, unless the user named it.
3. For an `edited` model, ask for each conflicting key whether to keep the user's value (`mine`) or take the gallery's (`gallery`). Without a choice for every conflicting key the upgrade is refused.
4. Summarise what will happen and wait for confirmation (safety rule 1): the model stops, the new files download beside the old ones, the new version loads as a check, and a failed check puts the old version back. Then call `upgrade_model` with `action` `upgrade` (or `config_only` when the user wants only the config and templates), `name` and `choices`.
5. Poll `get_job_status` with the returned `job_id` until done and report the outcome. On failure, quote the error and offer `action` `retry`.
6. After a confirmed upgrade, say that the old files stay on disk until removed, and offer `action` `remove_old` (confirm first).
7. To keep a model at its version, confirm, then call `action` `hold`; `unhold` releases it.
8. A model in state `unknown` with reason `legacy-sidecar` was installed by an earlier release, which did not record its origin. Offer to track it: confirm, then call `action` `adopt`. Its config on disk does not change. If the answer lists candidates (`gallery@entry`), ask which one and call `adopt` again with `gallery` (and `entry`). Then run `model_updates` with `action` `check`.
