+++
url = "/features/plugins/"
disableToc = false
title = "Plugins"
weight = 7
description = "Install plugins that are compatible with the Claude Code plugin format from marketplaces, pinned to a commit."
keywords = ["plugins","marketplace","skills","commands","agents"]
devbuild = "feature"
+++

LocalAI installs plugins that are compatible with the Claude Code plugin format. A plugin is a folder of skills, slash commands, sub-agents, MCP servers and hooks. A marketplace is a git repository or a JSON file that lists plugins and where to get them.

This page describes what the server does in this version. The REST API, the `local-ai plugins` command and the **Plugins** tab of the Add-ons page come in later versions; until then the server keeps its marketplaces and refreshes them, and nothing is installed from the UI.

The names `CLAUDE_PLUGIN_ROOT` and the other `CLAUDE_*` variables are names of the format: plugins read them, so LocalAI keeps them.

## Marketplaces

A marketplace is one of:

| Source | Example | How the server reads it |
|---|---|---|
| `owner/repo` | `acme/plugins` | A partial, sparse clone of `https://github.com/owner/repo.git` that downloads `.claude-plugin/marketplace.json` only. |
| An `https` git URL | `https://git.example.com/team/plugins.git` | The same, from that host. |
| An `https` URL of a `marketplace.json` file | `https://example.com/.claude-plugin/marketplace.json` | One HTTP GET, at most 1 MiB, with no redirect off `https`. Plugins listed with a relative source cannot be installed from it. |

`ssh://`, `git@`, `file://`, local paths and `http://` are refused.

On its first start, a server adds the curated LocalAI marketplace with the ID `localai`. A release build reads the list at its own release tag; any other build reads it on `master`. The flag `--plugin-marketplaces` (`LOCALAI_PLUGIN_MARKETPLACES`) replaces that list at the first start: comma-separated sources, each optionally `id=source`, for example `team=acme/plugins,https://example.com/.claude-plugin/marketplace.json`. The flag is read only while the server has no marketplace at all, so it never adds sources to a running server. A default marketplace that an administrator removes stays removed.

The switch **Use official libraries** (`official_libraries`) also suspends the default marketplaces: they stay in the list and are not read until it is on again.

The server refreshes each marketplace every 6 hours. A refresh that fails keeps the last good list and records a short error code. A refresh never installs or updates anything; it marks the installs that have a newer version.

Entries with an `npm`, `archive` or `command` source are listed as not supported.

## Installs

An install is pinned to a full commit SHA, and its files are stored once under their content digest. Before an install, the server fetches the plugin into a scratch folder, checks every file (no path that leaves the folder, no device or link out of the tree, size and count limits), computes the digest, and reads the plugin. The person who installs reviews the commit, the digest and what the plugin contributes. The install fetches the plugin again at the same commit and refuses it when the files differ from the reviewed ones.

A plugin runs code when it has a command hook, a stdio MCP server, files in `bin/`, an LSP server, a monitor, or a shell injection in a skill or a command. Who can install:

| Action | User | Administrator |
|---|---|---|
| Install a plugin that runs no code, for themselves | yes, when `plugins_user_installs` is on | yes |
| Install for everyone | no | yes |
| Install a plugin that runs code | no | for everyone only, from the browser |

An install for everyone of a plugin that users installed for themselves replaces their installs; it stays on for them. An update of a personal install whose new version runs code is refused, and the install keeps its version. An update that adds code to an install for everyone turns it off for each user until they turn it on again.

Plugin text is data from its author: skills and commands go into the prompt like any other skill. Community plugins are not signed; the curated marketplace is reviewed by people and is not signed either.

## In a chat

The plugins that contribute to a chat are the ones installed for everyone or for its user, enabled by that user, not switched off for the chat, and, in a chat with a saved agent, granted by the agent. The server reads this at each turn. A chat sees the stored copies of the plugins, read only, at `/workspace/shared-plugins/<digest>/`, the value of `CLAUDE_PLUGIN_ROOT`.

Each user turns an install for everyone on for themselves; it starts off. A personal install starts on for its owner. Turning a plugin off keeps its settings. When an update adds code to an install for everyone, it stays off for each user until that user turns it on again with the explicit re-enable, which accepts the code.

A chat keeps the plugins it switched off (`plugins_off`, install IDs, at most 200), so a plugin you enable later is on in chats that exist already. A saved agent has an exact grant list, `plugins`: an agent with an empty list gets no plugin, and a chat switch only narrows what the agent grants. An exported agent keeps its list.

| Component | In a chat |
|---|---|
| Skills | The plugin layer of the chat's skills, named `<plugin>:<skill>`. See [Skills]({{% relref "/features/chats-and-agents/chats-skills#skills" %}}). |

## Settings

| Setting | Default | Meaning |
|---|---|---|
| `plugins_user_installs` | `true` | Users can install plugins that run no code for themselves. |
| `plugins_model_tiers` | `{}` | The model of each agent tier of the format (`haiku`, `sonnet`, `opus`). An unmapped tier uses the chat's model. |
| `plugins_hook_timeout_seconds` | `60` | The longest one hook handler may run, 1 to 600 seconds. |
| `plugins_max_bytes` | `20971520` | The largest size of the files of one plugin, 1 MiB to 200 MiB. |

Each setting applies at once. Plugins need a data path: the server fetches into `<data path>/plugins/scratch/`, never into the system's temporary folder, and keeps its records in the database, or in `<data path>/plugins/store.json` when there is none.

## With several frontends

| State | Mode | How |
|---|---|---|
| Marketplaces, their lists, packages, installs, the state of each user, review tokens | Stateless per request | Rows in the database (or the file store of a server with no database). Every frontend reads them per request. |
| Package files | Shared, immutable | The workspace store, one folder per digest. |
| Marketplace refresh and removal of unused packages | One frontend at a time | An advisory lock taken for each pass. A marketplace refreshed less than 6 hours ago is skipped, so leadership need not stay on one frontend. |
| Install, update or removal of one plugin | One at a time per plugin | A lock on the plugin ID across frontends, held for the length of the change. |
| Install and update jobs | Per frontend | The job runs on the frontend that took the request and shows on every frontend's operations list. A restart fails the job; it is not resumed. |
| Fetch scratch folders | Per frontend | Under the data path, removed when the fetch ends and at startup. |
| Model tiers and limits | Shared | Runtime settings. |
