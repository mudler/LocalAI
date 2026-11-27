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

This page describes what the server does in this version. The server keeps its marketplaces, refreshes them, and installs plugins through the [REST API](#rest-api). The [`local-ai plugins`]({{% relref "/reference/cli-reference#plugins" %}}) command uses the same API. The [**Plugins** tab](#the-plugins-tab) of the Add-ons page is the same in the web UI.

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

## The curated marketplace

The default marketplace `localai` is a list that the LocalAI maintainers review. It lives in the LocalAI repository, in [`plugins/`](https://github.com/mudler/LocalAI/tree/master/plugins), and the server reads it as one file:

```
https://raw.githubusercontent.com/mudler/LocalAI/<ref>/plugins/.claude-plugin/marketplace.json
```

`<ref>` is the release tag for a release build, and `master` for any other build. At each start the server points the `localai` marketplace at the file of its own build, so after an upgrade it reads the tag of the new release; a removed `localai` marketplace stays removed. A release server therefore sees new entries and new pins with the next release. To get them sooner, add the `master` URL as a second marketplace.

Each entry points to the plugin's own repository at a full commit SHA. Nothing is copied into the LocalAI repository, except the `localai` plugin, which is kept there. Each entry also says, under `metadata.localai`, the SPDX license ID, whether the plugin runs code, and the date of its last review. The first list has plugins that run no code:

| Plugin | Source | Adds |
|---|---|---|
| `localai` | LocalAI, `plugins/localai` | Skills `localai:api`, `localai:models`, `localai:addons`; command `/localai:install-model <name>` |
| `frontend-design` | `anthropics/claude-plugins-official` | One skill to design and build web interfaces |
| `python-development` | `wshobson/agents` | Python skills (layout, testing, typing, packaging, async, performance) and a scaffold command |
| `developer-essentials` | `wshobson/agents` | Skills on git, debugging, code review, error handling, SQL, end-to-end tests, auth and monorepos |
| `documentation-standards` | `wshobson/agents` | Two skills on documentation that people and models both read |
| `shell-scripting` | `wshobson/agents` | Skills on defensive Bash, ShellCheck and Bats tests |
| `unit-testing` | `wshobson/agents` | A command that generates unit tests |

Some of these plugins also have sub-agents. LocalAI lists them and does not use them yet.

The `localai` plugin is for agents that use a LocalAI server from outside, through its HTTP API and CLI. Inside a LocalAI chat, the built-in `localai` skill and the client tools already do this, so the plugin starts off for each user.

### Propose a plugin

Open a pull request that adds the entry to `plugins/.claude-plugin/marketplace.json`. The rules and a trial checklist are in [`plugins/README.md`](https://github.com/mudler/LocalAI/blob/master/plugins/README.md). In short:

- the source is `github` or `git-subdir`, pinned to a full commit SHA;
- `metadata.localai` has `license`, `runsCode`, `phase` and `reviewed`;
- the pull request has the result of a trial with a local model: each skill and each command, one line each.

CI checks the schema, the pins and the license file at the SHA. It then fetches each entry at its SHA and runs `local-ai plugins validate --strict` on it, which fails when `runsCode` differs from what the checks find. A change to the `localai` plugin keeps its old pin until a bot pull request pins it to the merge commit.

## Installs

An install is pinned to a full commit SHA, and its files are stored once under their content digest. Before an install, the server fetches the plugin into a scratch folder, checks every file (no path that leaves the folder, no device or link out of the tree, size and count limits), computes the digest, and reads the plugin. The person who installs reviews the commit, the digest and what the plugin contributes. The digest covers the files and what the plugin contributes once its marketplace entry is merged in, so two marketplace entries that point at the same files but add different components are two packages. The install fetches the plugin again at the same commit and refuses it when the files or the marketplace entry differ from the reviewed ones.

A plugin runs code when it has a command hook, an http hook (it sends the hook data, tool inputs included, to a URL), a stdio MCP server, an MCP server with a `headersHelper` command, any file in `bin/` (dotfiles too), an LSP server, a monitor, or a shell injection in a skill or a command. The review shows the command and arguments of each hook and the URL of each http hook, and lists the host of an http hook with **Connects to `<host>`**. Who can install:

| Action | User | Administrator |
|---|---|---|
| Install a plugin that runs no code, for themselves | yes, when `plugins_user_installs` is on | yes |
| Install for everyone | no | yes |
| Install a plugin that runs code | no | for everyone only, from the browser |

The install for everyone has a new install ID, so the chat switches and the agent grants that named a replaced personal install do not carry over: the plugin is on again in those chats, and an agent must be granted it again. Two plugins with the same name cannot both contribute to one person, because their skills and commands would have the same names: an install is refused with `409 conflict` when a plugin of that name from another marketplace is installed for everyone, or for the same person, and an install for everyone is refused when any install has that name. An install for everyone of a plugin that users installed for themselves replaces their installs; it stays on for them. An update of a personal install whose new version runs code is refused, and the install keeps its version. An update that adds code to an install for everyone turns it off for each user until they turn it on again.

Plugin text is data from its author: skills and commands go into the prompt like any other skill. Community plugins are not signed; the curated marketplace is reviewed by people and is not signed either.

## The Plugins tab

The **Plugins** tab of the Add-ons page shows to everyone who has the `plugins` feature. It has three views.

- **Browse** lists the plugins of the active marketplaces as cards, with a search, a marketplace filter, a category filter and **Hide code plugins**. A card shows what the plugin adds once that is known: the number of skills, commands, agents and hooks, and **Connects to `<host>`** for each remote MCP server. A plugin that runs code shows **Runs code** and **Needs Sandbox**: its code runs only in the Sandbox of a chat. Whether a plugin runs code comes from an install of it, else from the last review of it by anyone (which also gives what it adds), else, for the curated marketplace only, from its `runsCode` claim, which the card marks **Per the curated list**. With none of these the card says **Not checked yet**. The review of the version to install always decides. A user sees **Install** on a plugin that is not known to run code; a plugin that runs code says that an administrator can install it.
- **Installed** lists the installs for everyone and your own, each with your switch, **Update** when a newer version waits, and a menu with **Details**, **Settings**, **Roll back** and **Remove**. Details list the components, the components that are not supported with the reason, and the code files as text. Settings edit the plugin's settings that are not secret; secret settings come in a later version. An install whose update added code shows "This plugin now runs code in your Sandbox" and stays off until you press **Turn on again**.
- **Marketplaces** lists the marketplaces with their state and when each was read. Administrators add, refresh, turn off and remove them.

**Install** opens a review. LocalAI fetches the plugin and checks it, then shows "Community plugin, not signed", the commit, the digest, what it adds and, for a plugin that runs code, the full text of each code file. Such a plugin needs a tick that you read the code and accept that it runs in the Sandbox, and it is installed for everyone. An administrator chooses **Everyone** or **Only me** for a plugin that runs no code. The install runs in the background. An administrator follows it on the operations list. A user, who cannot see that list, gets a message on the Plugins tab when the install shows in the Installed list. When it fails or is cancelled, or the wait runs out, the notifications say how it ended. The person who started an install or an update always gets a notification of its end.

**Update** opens the update review: the commits, the files that change with their line counts, and the diff of each code file. When the code of an install for everyone changed, each changed code file needs its own tick before **Update**. The code changed when a code file, a code reason or a code component (a command or http hook, a local MCP server, a `headersHelper` command, a `bin/` file) changed. The review also shows, in full, each file that did not change but runs now (for example a script that a new hook of the marketplace entry calls), and lists the hooks, servers and programs that the new version adds; these need a tick too.

The review link that `local-ai plugins install` and the LocalAI Assistant give for a plugin that runs code, `/app/addons?tab=plugins&plugin=<name@marketplace>`, opens the review of that plugin.

## In a chat

The plugins that contribute to a chat are the ones installed for everyone or for its user, enabled by that user, not switched off for the chat, and, in a chat with a saved agent, granted by the agent. A trigger without an agent keeps off the plugins that its source chat switched off. A user whose `plugins` feature an administrator turned off gets no plugin in any chat, their own installs included, from the next turn on. The server reads this at each turn. A chat sees the stored copies of the plugins, read only, at `/workspace/shared-plugins/<digest>/`, the value of `CLAUDE_PLUGIN_ROOT`.

Each user turns an install for everyone on for themselves; it starts off. A personal install starts on for its owner. Turning a plugin off keeps its settings. When an update adds code to an install for everyone, it stays off for each user until that user turns it on again with the explicit re-enable, which accepts the code.

A chat keeps the plugins it switched off (`plugins_off`, install IDs, at most 200), so a plugin you enable later is on in chats that exist already. In the composer, the **Skills** menu lists the skills of each plugin under **Plugin: `<name>`** with one switch for the plugin in this chat, and the **Tools** menu has the same switch for each plugin that has connectors. The agent editor has a **Plugins** list. A saved agent has an exact grant list, `plugins`: an agent with an empty list gets no plugin, and a chat switch only narrows what the agent grants. An exported agent keeps its list.

| Component | In a chat |
|---|---|
| Skills | The plugin layer of the chat's skills, named `<plugin>:<skill>`. See [Skills]({{% relref "/features/chats-and-agents/chats-skills#skills" %}}). |
| Commands | `/<plugin>:<command> <arguments>`. See [Run a plugin command]({{% relref "/features/chats-and-agents/chats-skills#run-a-plugin-command" %}}). A command that runs a shell command needs a later version. |
| MCP servers (`http`, `sse`) | One connector per server and user, owned by the plugin. See [Connectors of a plugin]({{% relref "/features/chat-apps/mcp-servers#connectors-of-a-plugin" %}}). A `ws` server is not supported; a `stdio` server comes in a later version. |

## REST API

All routes are under `/api/plugins` and need the `plugins` feature, which is on for every user by default. An administrator can turn it off for a user on the Users page. The routes answer `503` with the type `plugins_unavailable` and the reason when the server has no data path or no chat files.

| Method and path | Who | What it does |
|---|---|---|
| `GET /api/plugins/marketplaces` | user | Lists the marketplaces, with their state and the code of the last failed refresh. |
| `POST /api/plugins/marketplaces` | administrator | Adds a marketplace and reads it at once: `{"source": "owner/repo", "ref": "", "id": ""}`. |
| `PUT /api/plugins/marketplaces/{id}` | administrator | Changes `ref` or `enabled`. |
| `DELETE /api/plugins/marketplaces/{id}` | administrator | Removes a marketplace. The installs from it stay. |
| `POST /api/plugins/marketplaces/{id}/refresh` | administrator | Reads the marketplace now. |
| `GET /api/plugins/catalog` | user | Browses the active marketplaces: `q`, `marketplace`, `category`, `runs_code`, `offset`, `limit` (at most 200). |
| `GET /api/plugins/catalog/{marketplace}/{name}` | user | One entry, with the installs of it that the caller can see, `runsCode` with its source `runsCodeFrom` (`install`, `review` or `curated`), and `reviewed`, the last review of the plugin (commit, digest, code reasons, what it adds). The list has the same fields. |
| `POST /api/plugins/inspect` | user | `{"plugin": "name@marketplace"}`: fetches, checks and reads the plugin, and returns the preview with a token. |
| `GET /api/plugins/inspect/{token}/files/{path}` | user | One file of the preview, as plain text. |
| `POST /api/plugins/installs` | user or administrator | `{"token": "...", "scope": "user"}`. Answers `202` with `jobID`. |
| `GET /api/plugins/installs` | user | The installs for everyone and the caller's own, with the caller's switch. Administrators add `?all=true`. |
| `GET /api/plugins/installs/{id}` | user | One install: components, skipped components, code files, hosts. |
| `GET /api/plugins/installs/{id}/files/{path}` | user | One file of the installed plugin. |
| `POST /api/plugins/installs/{id}/update/preview` | owner or administrator | The files that change, the diffs of the code files, the files that became code (`newCodeFiles`), the added code components (`codeItems`), and a token. |
| `POST /api/plugins/installs/{id}/update` | owner or administrator | `{"token": "...", "approved_files": [...]}`. Answers `202` with `jobID`. |
| `POST /api/plugins/installs/{id}/rollback` | owner or administrator | Sets the previous version again. |
| `DELETE /api/plugins/installs/{id}` | owner or administrator | Removes the install and the switches of its users. |
| `GET /api/plugins/installs/{id}/me` | user | The caller's switch and settings. |
| `PUT /api/plugins/installs/{id}/me` | user | `{"enabled": true, "reenable": false, "config": {...}}`. |
| `PUT /api/chats/{id}/plugins` | user | `{"off": ["<install ID>"]}`: the installs switched off in one chat, at most 200. |

A preview token works once, for 15 minutes, and only for the caller who made the preview. The install fetches the plugin again at the same commit and fails when the files differ from the reviewed ones. Its progress is in `GET /api/operations`, where the row has `isPlugin: true`, and `POST /api/operations/{jobID}/cancel` cancels it.

The install or the update of a plugin that runs code needs the administrator's own browser session. The request must be a same-origin request of the web UI, without an API key, and carry the header `X-LocalAI-Confirm` with the plugin ID (install) or the install ID (update and rollback). A request with an API key or from an MCP client gets `403` with the type `not_human_surface`.

An install for everyone starts off for each user. A user turns it on with `PUT /api/plugins/installs/{id}/me`. When an update adds code, the install stops for each user and the request to turn it on again answers `409 needs_reenable` until it sends `"reenable": true`. Secret settings of a plugin are not supported yet (`422`).

Errors use the usual error body; `error.type` says why:

| Status | `error.type` | Meaning |
|---|---|---|
| 400 | `invalid_token` | The preview token is unknown, expired, used, or another user's. Inspect again. |
| 400 | `invalid_request` | A field, a source, a ref or a path is not valid. |
| 403 | `needs_admin` | A user installs a plugin that runs code, or personal installs are off. |
| 403 | `code_for_everyone` | A plugin that runs code is installed for everyone only. |
| 403 | `blocked` | The new version of a personal install runs code. The install keeps its version. |
| 403 | `not_human_surface` | The change needs the administrator's browser session. |
| 403 | `forbidden` | The caller may not do this. |
| 404 | `not_found` | No such marketplace, entry, install or file. A personal install of another user is not found. |
| 409 | `conflict` | The plugin is installed already, the install changed meanwhile, or another plugin with the same name is installed where both would contribute (from another marketplace); the message names that install. |
| 409 | `instance_installed` | The plugin is installed for everyone; `error.param` is that install. |
| 409 | `no_update` | The install is at the latest version. |
| 409 | `needs_reenable` | The install now runs code; send `"reenable": true`. |
| 422 | `unsupported` | A source type that LocalAI does not fetch, or a secret setting. |
| 422 | `content_refused` | A file of the plugin failed the checks. |
| 422 | `limit_reached` | Too many marketplaces or installs. |
| 429 | `rate_limited` | Too many reviews: a user may inspect 6 plugins in a row, then one more every 10 seconds, and one frontend runs at most 4 inspects at once. Administrators have only the second limit. |
| 502 | `fetch_failed` | The plugin or the marketplace could not be read. The server log has the cause. |

## In the LocalAI Assistant

The admin tools of the [LocalAI Assistant]({{% relref "/features/chats-and-agents/localai-assistant" %}}) and of `local-ai mcp-server` have one tool, `plugins`, with an `action`:

| Action | Fields | What it does |
|---|---|---|
| `list_marketplaces` | | Lists the marketplaces. |
| `add_marketplace` | `source`, `ref`, `marketplace` (the ID) | Adds a marketplace and reads it. Administrators. |
| `refresh_marketplace` | `marketplace` | Reads a marketplace now. Administrators. |
| `browse` | `query`, `marketplace` | Searches the catalog. |
| `list_installed` | `all` | Lists the installs and the caller's switch. |
| `install` | `plugin`, `scope` | Inspects the plugin, then installs it. A plugin that runs no code only. |
| `check_updates` | `id` | Without `id`: the installs with an update or a blocked update. With `id`: the files that change and whether the code changes. |
| `enable`, `disable` | `id` | Turns an install on or off for the caller. |
| `remove` | `id` | Removes an install. |

The tool acts as the person whose chat runs it, with the rights of the REST API. It never has the administrator's browser session, so it does not install a plugin that runs code, apply an update or roll back. For a plugin that runs code, the tool answers with an error that gives the review page, `/app/addons?tab=plugins&plugin=<name@marketplace>`. It also does not turn on an install that gained code: accepting the code is a person's act on the Plugins tab. The read-only flavour of `local-ai mcp-server` keeps the read actions and refuses the others.

In a LocalAI chat, the actions `add_marketplace`, `install`, `enable`, `disable` and `remove` do not run when the model calls them. Each call shows a confirmation card in the chat with the action and its target, and the action runs, as the owner of the chat, only when the person confirms the card. The reason is that the model reads text from marketplaces, which anyone can publish, and a description can hold instructions. For the same reason, `browse` and `list_marketplaces` return that text under `marketplace_data` with a note that it is data, not instructions. The read actions and `refresh_marketplace` run at once. `local-ai mcp-server` has no cards: an MCP client acts with the rights of its own API key, and the client must ask for confirmation itself.

## Security

- Plugins are community content and are not signed. Every review shows "Community plugin, not signed", the commit and the digest. The curated marketplace is reviewed by people and is still not signed.
- The text of skills and commands goes to the model like any other skill text. Text from marketplaces (names, descriptions) is written by third parties; the assistant gets it as data and confirms every plugin change on a card (see [In the LocalAI Assistant](#in-the-localai-assistant)).
- The server fetches plugins and marketplaces over https only, and refuses a location on a private or internal network (localhost, a private address, or a name that resolves to one) unless an administrator allows such addresses with `mcp_connectors_allow_private`, the setting of the MCP connectors. The fetch of a `marketplace.json` URL checks the address it connects to; git resolves the name again when it connects, so the check of a git location is done before the fetch.
- A plugin that runs code is installed, updated or rolled back only for everyone, by an administrator, from the administrator's browser session (`X-LocalAI-Confirm`, a same-origin request of the web UI, no API key). Plugin code runs only in the Sandbox.

**Limit with authentication off.** On a server without authentication, every caller is the anonymous administrator, and the browser-session check is a set of request headers: `X-LocalAI-Confirm` and the `Sec-Fetch-*` headers that a browser sends. A browser adds them by itself, but any program can send them too. So on such a server, any process that can reach the LocalAI port can install a plugin that runs code, approve a code update or roll back. Examples are a script on the same machine, a command that a model runs with the host backend of the Sandbox (which runs commands on this machine without isolation), or a host on a network that reaches the port. The add-on host grants use the same check and have the same limit. With authentication on, the request also needs the administrator's session cookie.

To limit this:

- Turn on authentication on any server that more than one person or program can reach.
- Bind LocalAI to the loopback address, or put it behind a firewall, when authentication is off.
- Use the Sandbox with KVM or gVisor, not the host backend. A sandbox of these backends has no network device, and its proxy lets through only the hosts that the chat's network rules allow.

## Settings

An administrator sets `plugins_user_installs` and `plugins_max_bytes` on the Settings page, under **Tools**, or through `/api/settings`. The other settings are set through `/api/settings` only.

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
| Last review of each plugin | Shared | A row per plugin ID in the database, replaced by each inspect. |
| Marketplace refresh and removal of unused packages | One frontend at a time | An advisory lock taken for each pass. A marketplace refreshed less than 6 hours ago is skipped, so leadership need not stay on one frontend. |
| Install, update or removal of one plugin | One at a time per plugin | A lock on the plugin ID across frontends, held for the length of the change. |
| Connectors of plugins | Stateless per request | Rows of the connectors table. Turning a plugin on or off writes them; each turn makes them match the package again and writes nothing when they match, so any frontend can do it. MCP sessions stay per frontend, as for every connector. |
| Install and update jobs | Per frontend | The job runs on the frontend that took the request and shows on the operations list of the administrators. Its end is a notification to the person who started it. A restart fails the job; it is not resumed. |
| Fetch scratch folders | Per frontend | Under the data path, removed when the fetch ends and at startup. |
| Model tiers and limits | Shared | Runtime settings. |
