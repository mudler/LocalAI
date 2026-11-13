---
description: Install a model from a HuggingFace link, an OCI reference, a file path or an HTTP URL.
---
# Skill: Import a model from a URI

Use this when the user wants to install a model from a source LocalAI doesn't already curate — a HuggingFace link, an OCI image reference, a `file://` path, or a generic HTTP URL. Prefer `gallery_search` + `install_model` first if the model is in a configured gallery; only fall back to import when it isn't.

If the user only names a model and `gallery_search` finds nothing, call `search_models` with their text. Show the hits with provider, size and downloads or seeders (and say when a hit is `gated`), let the user pick one and call `inspect_remote_model` with its `uri` to see the quantizations, sizes and the backend. Use `estimate` to check that it fits. Then import with the result's `uri` and the variant's `preference` as `quantization`. A provider with `ok: false` in `providers` is down: say so, do not retry in a loop. A `pirateface://` uri needs BitTorrent support on the server; if the import fails, report the error.

1. If the user has not already supplied a URI, ask for one. Acceptable forms include `Qwen/Qwen3-4B-GGUF`, `https://huggingface.co/...`, `oci://...`, or `file:///path/to/local.yaml`.
2. Summarise: "I'll import `<URI>` — confirm?" and wait for confirmation.
3. On confirmation, call `import_model_uri` with the URI and **no** `backend_preference`.
4. **If the response has `ambiguous_backend == true`:**
   - Show the user the `backend_candidates` list as a numbered choice, mention `modality` if present, and quote any returned `hint` verbatim.
   - Wait for the user to pick.
   - Call `import_model_uri` again with the URI and `backend_preference` set to the chosen candidate.
5. **If the response has a `job_id`:**
   - Note the `discovered_model_name` if present (the assistant should use that name for follow-ups, since the importer may rewrite it).
   - Poll `get_job_status` until the job reports `processed: true`. Report meaningful progress changes.
6. After success, call `reload_models`, then `list_installed_models` to confirm the new model is visible. Tell the user the canonical name to use in chat completions.

If `import_model_uri` returns a non-ambiguity error (network, gated repo, unsupported source), surface it verbatim and ask whether to retry, try a different URI, or abort. Never re-call with a guessed backend — only use `backend_candidates` from a real ambiguity response.
