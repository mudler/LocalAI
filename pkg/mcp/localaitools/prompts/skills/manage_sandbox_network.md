# Skill: Manage the network of the Sandbox commands

Use this when the admin asks whether chats can install packages, what the commands of the chats reached on the network, or wants to open or close the network of the Sandbox.

The commands a chat runs in the Sandbox reach the network only through a proxy. Each chat has a preset: `off` (nothing), `registries` (the package registries, such as PyPI, npm, Go modules, crates.io, RubyGems, Maven Central and GitHub releases) or `all` (every public host). The ceiling is the widest preset any chat may use. Commands that run directly on this machine (the built-in bash) are not covered: they have the network of the server.

1. Call `get_chat_egress` first. Report the ceiling, the default of a new chat, whether a Sandbox add-on runs commands, and the allowed and blocked requests of the last 24 hours.
2. To see what commands reached or tried to reach, call `list_egress_events`. Filter by `verdict` `block` to explain why a package install failed, or by `host` or `user`. Summarise; do not paste every line.
3. To change the ceiling, say what changes and wait for confirmation (safety rule 1). For example: "I'll set the network ceiling of the chats to **registries**, so commands can reach the package registries only — confirm?" Then call `set_chat_egress_ceiling`. Say that `all` lets commands reach any public host, which can carry the user's files out.
4. `get_chat_egress` also lists `allowed_hosts`: exact hosts (or `host:port`) that every chat with the network on may always reach, with the reason `allowed_host` in the audit. With the ceiling off they do nothing. You cannot change the list: tell the admin to edit "Always allowed hosts" in Settings > Tools > Sandbox network.
5. You cannot let commands reach private network addresses. That switch needs a typed confirmation: tell the admin to use Settings > Tools > Sandbox network.
6. A user changes the preset of one chat in the chat's Tools menu, up to the ceiling. You do not change a chat's preset.
7. On an error, quote it verbatim (safety rule 3) and stop.
