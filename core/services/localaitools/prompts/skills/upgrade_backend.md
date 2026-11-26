---
description: Upgrade one backend, or several backends and models in one run.
---
# Skill: Upgrade a backend

Use this when the user asks to upgrade, refresh, or update a backend.

1. Call `list_backends` to confirm the backend is installed and to capture its canonical name.
2. If the user asked generically ("upgrade the backends"), call `backend_updates` with `action` `check` to get the backends that have a newer build. Present them as a numbered list and ask which to upgrade. If a source shows an error or was never checked, say so; do not say that everything is up to date.
3. Summarise: "I'll upgrade **`<name>`** — confirm?" and wait.
4. On confirmation, call `upgrade_backend` with the canonical name.
   If the call fails with "backend installation is disabled", the server is
   configured never to install or upgrade backends itself (they are managed
   outside LocalAI); tell the user that and stop — do not retry or try
   `install_backend` instead.
   If it fails with `not_installed` or `no_update`, tell the user the backend
   is not installed or has nothing newer, and stop. If it fails with
   `run_active`, an update run is running: offer `upgrade_run_status` and stop.
5. Poll `get_job_status` until done. Report progress and the final outcome.
6. After success, recommend the user reload any model that was using the upgraded backend (or call `reload_models` if the user agrees).

## Several backends and models at once

Use this when the user asks to update several things at once ("update everything", "upgrade all backends").

1. Call `backend_updates` and `model_updates` with `action` `check`. If a source failed or was never checked, say so; do not say that everything is up to date.
2. List what has an update as a numbered list: kind (`gallery`, `library` or `model`), name, from and to. Say that a model upgrade stops the model while it switches. Leave out held models and models whose update changes edited keys: they need `upgrade_model` with choices.
3. Add-ons are never part of an assistant run. If the user asks for an add-on, tell them to update it from the Updates page or the add-on page.
4. Summarise the run and wait for an explicit confirmation.
5. On confirmation, call `upgrade_run_start` with the items, each with `expected_to` set to the version or digest you showed.
6. If it fails with `run_active`, another run is running: offer `upgrade_run_status` or `upgrade_run_cancel` and stop. If it fails with `stale` or `no_update`, run step 1 again and ask again.
7. Poll `upgrade_run_status` with the run id until the run state is `finished` or `cancelled`, at most 30 polls. Report each item: `updated`, `failed` (with `error_key` and `error`), `skipped` or `unconfirmed`.
