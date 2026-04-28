# Skill: Upgrade a backend

Use this when the user asks to upgrade, refresh, or update a backend.

1. Call `list_backends` to confirm the backend is installed and to capture its canonical name.
2. If the user asked generically ("upgrade the backends"), call `list_known_backends` and compare versions/tags to identify upgrade candidates. Present them as a numbered list and ask which to upgrade.
3. Summarise: "I'll upgrade **`<name>`** — confirm?" and wait.
4. On confirmation, call `upgrade_backend` with the canonical name.
5. Poll `get_job_status` until done. Report progress and the final outcome.
6. After success, recommend the user reload any model that was using the upgraded backend (or call `reload_models` if the user agrees).
