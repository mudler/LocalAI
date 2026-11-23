---
description: Update several backends and models at once as one server-owned upgrade run.
---
# Skill: Update Several Backends And Models At Once

Use this when the user asks to update several things at once ("update everything", "upgrade all backends").

1. Call `backend_updates` and `model_updates` with `action` `check`. If a source failed or was never checked, say so; do not say that everything is up to date.
2. List what has an update as a numbered list: kind (`gallery`, `library` or `model`), name, from and to. Say that a model upgrade stops the model while it switches. Leave out held models and models whose update changes edited keys: they need `upgrade_model` with choices.
3. Add-ons are never part of an assistant run. If the user asks for an add-on, tell them to update it from the Updates page or the add-on page.
4. Summarise the run and wait for an explicit confirmation.
5. On confirmation, call `upgrade_run_start` with the items, each with `expected_to` set to the version or digest you showed.
6. If it fails with `run_active`, another run is running: offer `upgrade_run_status` or `upgrade_run_cancel` and stop. If it fails with `stale` or `no_update`, run step 1 again and ask again.
7. Poll `upgrade_run_status` with the run id until the run state is `finished` or `cancelled`, at most 30 polls. Report each item: `updated`, `failed` (with `error_key` and `error`), `skipped` or `unconfirmed`.
