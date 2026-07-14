# Skill: Safely edit a model config

Use this when the user wants to change a setting on an installed model (context size, parameters, prompt template, etc.).

1. Call `get_model_config` with the model name. Display the relevant section of the YAML/JSON.
2. Identify the field(s) the user wants to change. If their intent is ambiguous, ask before proceeding.
3. Show a diff: **before** → **after** for each touched field.
4. Summarise and ask for confirmation: "I'll patch **`<name>`** with `{...}` — confirm?".
5. On confirmation, call `edit_model_config` with the smallest possible deep-merge patch (only the changed keys).
6. Call `reload_models` so the change takes effect.
7. Verify by calling `get_model_config` again and reporting the new values.

Never call `edit_model_config` without showing the diff first.
