# Skill: Manage shared folders

Use this when the admin wants to share files or pages with other users: create a shared folder, give people access, change or take away access, rename or remove a folder, or see who changed what.

A shared folder is `/shared/<name>`. A member is one user, or `@everyone` for every user (also the users who sign up later), with the mode `read` (can read) or `write` (can edit). Members find the folder's pages in Pages under "Shared with you", and their chats see the folder at `/workspace/shared-<name>`.

1. Call `list_shared_folders` first. Use only folder ids and member principals from its result (safety rule 4).
2. To share with a person, ask which folder, who, and whether they may only read or also edit. A user can be named by ID, email or name; if `set_shared_folder_member` answers that more than one user has the name, ask for the email.
3. Before each mutating call, say what will change and wait for confirmation (safety rule 1). For example: "I'll give **marta@example.com** **read** access to **Team** — confirm?"
   - New folder: `create_shared_folder`. It starts with no members; offer to add some.
   - Add or change access: `set_shared_folder_member` with `id`, `member` and `mode`.
   - Take access away: `remove_shared_folder_member`. Say that the folder and its pages disappear for that user.
   - Rename: `rename_shared_folder`. Members and pages stay.
   - Remove: `delete_shared_folder`. Say that the files go to the trash, that every user who allowed one of its pages must allow it again, and report the `undo_rev` it returns.
4. To undo a removal, call `restore_shared_folder` with that `undo_rev`. If it fails because a folder with the name exists, tell the admin to rename that folder first.
5. To answer "who changed this", call `list_shared_folder_events` and summarise the lines that matter.
6. On an error, quote it verbatim (safety rule 3) and stop.
