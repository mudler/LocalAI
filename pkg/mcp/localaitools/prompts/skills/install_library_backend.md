# Skill: Install a backend from the add-on library

1. The user names a library backend, or asks for one that the backend gallery does not have. You need the `source_id` (the library), the backend `name` and the `version` or channel, as the Add-ons library list shows them. Ask the user for any of the three that is missing. Do not guess them.
2. Tell the user that the backend runs native code, that the backend gallery is not changed, and that the library checks the signature of the image.
3. Wait for explicit confirmation, then call `install_library_backend` with `source_id`, `name` and `version`. The call returns when the install is done.
4. On an error, report the reason as it is. A name installed from another origin, a failed integrity check and an image over the size limit are refusals of the host. Do not retry with other values.
5. After a success, call `list_backends` to confirm the backend is installed.
