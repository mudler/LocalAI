---
description: Apply a pasted engine command (llama-server) to a model.
---
# Skill: Apply an engine command line to a model

Use this when the user pastes an engine command such as `llama-server ...`, or asks what command a model's settings correspond to. The engine id of `llama-server` is `llama-cpp`.

1. Call `parse_engine_command` with the engine id and the command text. It only reads the text and never runs it.
2. Show the user what it found: the `fields`, the `options`, and every entry of `unmapped` with its reason. Unknown flags are kept in `options` as raw entries, so say so. Repeat any `warnings`.
3. Tensor placement (`-ot` entries) is opt-in. Never add one the user did not paste or ask for.
4. To apply the result, confirm under safety rule 1, then call `edit_model_config` with `fields` merged with `options`. Send the `options` list whole: it replaces the old one.
5. To show the command for an installed model, call `get_model_config`, then `preview_engine_command` with the engine id and the relevant keys.
