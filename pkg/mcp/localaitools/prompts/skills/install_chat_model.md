# Skill: Install a chat model

Use this when the user wants to install a chat-capable model — including the case where there are no chat models installed at all and they ask you to bootstrap one.

1. Call `gallery_search` with their query (and `tag: "chat"` when the user asked specifically for chat).
2. Show the top results as a numbered list with name, gallery, short description, and license. If none match, say so and ask whether to broaden the search.
3. Wait for the user to pick.
4. Summarise the chosen install ("I'll install **`<gallery>/<name>`** — confirm?") and wait for confirmation.
5. On confirmation, call `install_model` with `gallery_name` and `model_name` from the chosen hit.
6. Poll `get_job_status` with the returned job id. Report meaningful progress changes (every ~10–20%, plus completion).
7. When the job reports `processed: true` and no error, call `reload_models`, then `list_installed_models` with `capability: "chat"` to confirm the model is now visible.
8. Tell the user the model is ready and how to use it (its name as the `model` field in chat completions).

If `install_model` fails, surface the error and ask whether to retry, pick a different model, or abort.
