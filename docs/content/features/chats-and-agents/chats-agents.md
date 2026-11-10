+++
url = "/features/chats-agents/"
disableToc = false
title = "Saved agents, triggers and inbox"
weight = 20
+++

A saved agent is a definition you can start a [chat]({{% relref "/features/chats-and-agents/chats" %}}) with.
It has instructions and four lists: the MCP servers, add-ons, collections and
skills the chat may use. Triggers and the inbox build on agents and are
described on this page too.

## Saved agents

An agent has:

| Field | Meaning |
|---|---|
| `name` | 1 to 80 characters. Unique for you, ignoring case. |
| `description` | A short note for you. |
| `instructions` | Text added to the system prompt of every turn. At most 32 KiB. |
| `model` | Optional. The chat uses this model. Chat-capable model aliases can also be selected; the agent saves the alias name, not its target name. Without it, the chat is created with the model you choose. |
| `mcp_servers` | Names of servers in the model's MCP configuration, and your [MCP connectors]({{% relref "/features/chat-apps" %}}#mcp-servers) as `connector:<id>`. The form lists both as a picker; it does not take free text. A save that adds a `connector:<id>` you have not defined, or, for an agent with a fixed model, a name that is not one of that model's MCP servers, is a `400` ("... is not one of your MCP connectors"). A name the agent already holds is kept, so an older agent still loads and saves; the form shows it as **Not defined** with a remove button, the Effective view says it is ignored, and the agent's chats skip it. |
| `addons` | IDs of installed add-ons. |
| `collections` | Names of your collections. |
| `skills` | Names of skills. The index of skills and the `load_skill` tool of the chat offer only these. |
| `builtins_off` | Groups of built-in tools the agent's chats never have: `shell`, `files`, `web`, `media`, `collections`, `subagents`, `triggers`, `changes`, `localai_client` (the inference tools) and `localai_admin` (the server-management tools). It is a ceiling: a chat with the agent shows the group switched off and cannot switch it on. Empty (the default): the chat has every group it has by default. A group this server does not offer is dropped. |
| `builtins_on` | Opt-in groups of built-in tools that chats with the agent start with: `saved_agents` and `localai_admin`. The chat can switch them off. `localai_admin` is for administrators and is never offered in a messaging chat or a run that starts by itself, whatever the agent says. |
| `network` | The network of the [Sandbox]({{%relref "/addons/contract-reference#network-access" %}}) commands of the agent's trigger and connector runs: `off` (the default), `registries` (the package registries) or `all` (every public host), capped by the administrator's `chats_egress_ceiling`. A run never shows a card: a host outside it is blocked, and the block is in the run's output. A chat you start with the agent starts with this preset too; you change it in the chat's Tools menu. The agent form sets it in the add-ons fold. |
| `folders` | Optional. Up to 8 folders of your files that chats with the agent work in, each with its own mode: `read` or `write`. `folder` is the same with one entry. Without them the agent works on all your files. See [Folders of an agent](#folders-of-an-agent). |

The agent form shows the same Tools list as the Tools menu of a chat, with a
short description under each switch: the built-in tool groups, the add-ons, your
connectors, and the skills. What you switch on is saved in `addons`, `mcp_servers`
and `skills`; what you switch off among the built-in groups is saved in `builtins_off`.

Each list is an exact grant. An empty list grants nothing: a chat with an agent
that names no add-ons has no add-on tools, even when you could use some. A
plain chat, with no agent, is not changed.

Document selections keep the names `docx`, `pptx`, `pdf`, and `xlsx`.
Selecting a skill does not grant its provider or command execution.
Grant Simple Office explicitly for its tool-based procedures; no command runner is required.
For Sandbox procedures, grant Sandbox and allow its selected, usable isolated `bash` tool.
A host command runner alone does not qualify.
Without an eligible provider, the selected skill is unavailable; no built-in document procedure serves instead.
An unavailable provider pin never silently selects another provider.
See [Document skills]({{%relref "/features/chats-and-agents/chats#document-skills" %}}) for provider choices and OFF preferences.

### Add-on changes apply at once

An add-on you turn on for a chat, or name in an agent, is allowed to change
your data in that chat. A tool call that adds rows, changes a schema, creates a
database or starts a job is applied at once, as you, with the same permission
and stale-data checks as the Add-ons page. There is no confirmation card. The
model is told what was applied, and the tool entry in the chat says it. A change the add-on kept a change set for can be undone from the
chat with an Undo button (Tables rows, schema and databases, and Fine-tuning and
Quantization job starts); the result says whether a change can be undone and, if
not, why. File changes of the Sandbox and Office add-ons are one change set in the
history of your files and can be undone there. A trigger or a scheduled run behaves the same, with no
proposal waiting for a browser. Other ways to call add-on tools, such as the
OpenAI-compatible API and MCP, still store a proposal that only your own
browser session can confirm.

### An agent never gives more than you have

A chat gets the intersection of the agent's lists and what you may use at that
moment. If an add-on is disabled, a collection is deleted or you lose the MCP
feature, the chat still starts. The thing is absent, and the model gets one
line at the start of the turn, for example `unavailable this turn: add-on
office, collection docs`. A sub-agent of the chat can only get tools the chat
has, and the groups of built-in tools that the chat switched off in its Tools
menu (for example **Your files**) stay off for the sub-agent too.

A chat can also start a saved agent as a sub-agent, when you switch **Saved
agents** on in its Tools menu. See [Saved agents as
sub-agents]({{%relref "/features/chats-and-agents/chats#saved-agents-as-sub-agents" %}}).

If you delete an agent, the chats that ran with it keep their history. For an
agent without a folder, their next turn runs without the agent's tools,
and the model is told that the agent was deleted. For an agent with a folder,
the next turn is refused instead; it never falls back to all your files.

### Pages

The Agents page (**Agents** in the sidebar, `/app/agents`) lists your agents with the chats that ran with
each one, and links to the **Triggers** and the [**Tasks**]({{% relref "/features/chats-and-agents/agent-jobs" %}}).
A page for one agent (`/app/agents/{id}`) shows its definition, the triggers that
start it and its run history from the server (the chats you had with it and the
ones its triggers and connectors started). Create and edit an agent in a form of
folds with a preview of the definition and of what changed. **Export** and
**Import** use the server's agent file (see below); the import opens the new
agent and says what the server changed. It also reads the JSON export of an
older agent, and names what it could not take over. With no agents yet, the
page offers the server's starter agents. Chat with an agent from its row: the
chat page opens empty with the agent chosen.

### API

```
curl -X POST .../api/chats/agents -d '{"name": "Reviewer", "instructions": "Answer in French.", "addons": ["office"], "skills": ["review"]}'
curl .../api/chats/agents
curl -X POST .../api/chats -d '{"agent_id": "<id>", "model": "my-model"}'
```

`GET`, `PUT` and `DELETE /api/chats/agents/{id}` read, replace and delete one.
A `PUT` replaces every field except `folders` (see below), the tool switches `builtins_off` and `builtins_on` and the [connections](#connections-of-an-agent), which it keeps when the body does not name them (an empty list or object clears them). Chats that already run with the agent use the new
definition from their next turn. Another user's agent is `404`, unless it is
[shared with you](#share-an-agent): then you may read it and chat with it, and
an edit, a delete or an export is `403`.

Agents need the `agents` feature, which is off by default for a user (see
[Chats permissions]({{% relref "/features/chats-and-agents/chats" %}}#permissions)). They have no administration
tools, as they are your own settings.

### Memory of an agent

The optional field `memory` chooses the notes of an agent: `mine_and_own` (reads your notes
and its own, saves to its own; the interface sets it for a new agent), `own`, `mine` or `none`.
The form shows the level from this field and the grant `memory.write`: `none` is Off, any
other choice without the grant is Reads notes, and any other choice with the grant is Reads
and saves notes. Nothing else is stored.
An agent without it is "as before": it reads notes when its add-ons name `memory`, and the
model chooses where a note goes. The host enforces the choice. A `PUT` without `memory`
keeps the stored one. `DELETE /api/chats/agents/{id}?memory=keep|export|delete` says what
to do with the agent's own notes (default `keep`). The optional field `memory_save` (`always`, `auto` or `never`) is the agent's own rule for saving a note: `always` and `never` can only be stricter than the person's setting, `auto` (Save it and tell me) lets this agent save at once even when the person's level is ask (the person's own always and never still hold), `ask` is refused with a 400, a `PUT` without it keeps the stored rule, and `use_mine` clears it. See [Short memory]({{% relref "/features/chats-and-agents/short-memory" %}}#memory-for-each-agent).

### What it can use

The agent form has one section, **What it can use**, with three sources and the
tool levels on top:

- **Built-in tools**: the groups LocalAI serves itself, with their options.
- **Add-ons**: one row for each add-on, with its switch, the account it uses,
  a **Right now** sentence, the skills that come with it and its tools (the ones
  that change data are marked).
- **MCP servers**: a picker over your defined MCP connectors and the MCP servers
  of the agent's model, one switch each, with a search field when there are
  many. They write the same `mcp_servers` list as before; they no longer sit
  under **Advanced**, and there is no free text.

A level (Look only, Assistant, Full) sets the built-in tools and permissions only.
It does not touch add-ons, accounts, MCP servers or skills.

**Right now** says what a chat of the agent gets from the add-on, by the same rule
the server uses to pick the account: an add-on that is off offers nothing; one
that needs no account just works; the accounts the agent names are used; with
none named, your only account is used; with two or more and none named, a chat
asks you to pick one before it gets any tool and a run with nobody there (a
trigger, a messaging chat) gets none. That last case, and an add-on with no
account at all, are shown in amber and in the Effective view as blocked.

Ticking an account switches the add-on on, as it does in a chat. Switching the
add-on off keeps the accounts, shown as **Kept for later**. Switching an add-on
on turns on the skills that come with it, with the same **Undo** and **Keep**
notice as a level; switching it off turns off only those skills. Nothing new is
stored for this: the skills that come with an add-on are read from the skill list.
Skills of an add-on show in the **Skills** list as "from" the add-on's title. The
skills that the memory provider ships are granted by the host with the notes;
the **Memory** section shows them once, without switches.

### Connections of an agent

An add-on whose tools run with the token of one of your
[connections]({{% relref "/features/chat-apps" %}}#github) lets the agent name which
accounts its chats may use. In the agent form, the add-on's row in **What it can
use** has an **Account it uses** list with a checkbox for each connection. A chat
of the agent uses that set, shows it read only, and cannot change it. With two or
more in the set, the model names the connection in each tool call (see [The
connections of an add-on]({{% relref "/features/chats-and-agents/chats" %}}#the-connections-of-an-add-on)). With
none ticked each chat chooses its own (with one connection, it is used); until a
person chooses, the add-on's tools are not offered. Many agents can use the same connection; the connection's row on its add-on page says how many do. The messaging connections that
answer with the agent are listed under **Identity**, read only.

Over the API an agent has `connections`, an object from add-on ID to a list of
binding IDs (`{"<add-on id>": ["<binding id>", ...]}`). Every binding must be
yours and belong to that add-on, else `400`. A single binding ID in place of the
list is read as a set of one. A `PUT` that does not name `connections` keeps
them, and `{}` clears them. The export file never carries them: a binding ID
belongs to one server, so the format and its version are unchanged, and an
imported agent has no connections until you choose some.

### Resources of an agent

Some add-ons keep their data in resources that you own or were granted, such as the
databases of Tables. An add-on can ask that each saved agent names the resources it
may use. In the agent form, the add-on's row in **What it can use** then lists your
resources under the add-on's own word for them ("Which databases it may use"), each
with a checkbox and a mode: **Read** or **Read and change**. Nothing ticked means the
agent can use none, and the row says so. The words come from the add-on's manifest
(`noun`, `noun_plural` and their translation); the form names no add-on.

Over the API an agent has `resources`, an object from add-on ID to a list of
`{"id": "<resource id>", "mode": "read" | "write"}`. A `PUT` that does not name
`resources` keeps the stored list, and `{}` clears it. A mode other than `read` or
`write`, a repeated resource or an ID that is not a resource ID is a `400`. The field
has no effect on an add-on that does not opt in. `GET
/api/chats/agents/addons/{addon}/resources` lists what you can give an agent for one
add-on (`items` with `id`, `name` and your own `role`, and the `noun` and `noun_plural`
the add-on uses; `lang` or `Accept-Language` picks a translation the add-on has). The
add-on rows of `GET /api/chats/draft/tools` and `GET /api/chats/{id}/tools` also carry
`tool_list` (the tools of the add-on, each `read` or `write`) and `resources` (the
words, set only for an add-on that opts in).

The host applies the list on every tool call that takes a resource, as one more link
after what you may use yourself (you own it, or it was shared with you):

- a call on a resource that is not on the list is refused, and the add-on is not
  called (code `resource_not_in_agent_list`);
- a call that changes a resource listed as `read` is refused
  (`resource_read_only_for_agent`);
- a tool that names no resource, such as one that creates a new one, is not affected;
- the list tool of the add-on shows the agent only the resources on the list, and a
  `read` resource shows as `viewer`;
- a chat of the agent cannot select a resource outside the list (`403`, type
  `resource_not_in_agent_list`), and a selection made earlier is checked again at each
  call;
- the chats that triggers and jobs of the agent start, and the sub-agents of a chat,
  use the same list. A sub-agent never reaches more than the agents above it;
- the host reads the list from the stored agent at each call, so a change holds at
  once.

In a messaging chat the tools that change things need the `tools.write` permission of
the connection, as for an add-on that holds a credential (see
[Connectors]({{% relref "/features/chat-apps" %}})); paired people have them without the tick
only for an add-on the agent is connected to. The access view and its preview say when
an agent whose add-ons only change resources has none to change
(`agent_resources_none`) or only read ones (`agent_resources_read_only`), and point at
the agent. The export file never carries `resources`: a resource ID belongs to one
server. An import drops the field and says so under `warnings`.

An add-on opts in with `capabilities.resources.agentScoped` in its manifest (feature
level 2; see [the manifest]({{% relref "/addons/contract-reference" %}}#feature-level)).

### Folders of an agent

`folders` gives the agent up to eight folders of your files, each with a mode, `read` (the
agent's chats can read it) or `write` (they can read and change it). `folder`, one `{path, mode}`, is read as a list with one entry. Responses carry `folders` in the order you gave them, and `folder`, the first of them:

```
curl -X POST .../api/chats/agents -d '{"name": "Acme", "folders": [
  {"path": "/Projects/Acme", "mode": "write"}, {"path": "/Reference", "mode": "read"}]}'
# {"id":"...","name":"Acme",...,
#  "folder":{"path":"/Projects/Acme","name":"Acme","mode":"write","effective":"write","state":"ok"},
#  "folders":[{"path":"/Projects/Acme",...},{"path":"/Reference","name":"Reference","mode":"read","effective":"read","state":"ok"}]}
```

- A list has at most 8 folders and each folder once. Nine folders, a repeat, a
  row without a path or a row without a mode is `400`.
- Where one folder is inside another folder of the same agent, the inner
  folder works at the mode of the outer one, and `effective` says so. The file
  store gives a folder the highest mode among the folders above it, so a read
  only folder inside a write folder cannot be kept read only, and a write
  folder inside a read only folder cannot be written. A folder in the shared
  folders is always read only.
- A folder that is `missing` or `unset` stops the agent, and the error names
  the row ("folder 2 was deleted..."). The other folders do not take its place.

- `path` is a folder of your files, as the Files page shows it. `/` is all of
  your files, which with `read` makes an agent that can read everything and
  change nothing. The folder cannot be `LocalAI`, `Chats` or a folder inside
  them, and it cannot be a shared folder. A path that is not a folder of yours
  is `400`.
- The server keeps the folder itself, not its path. When you rename or move
  the folder, the agent follows it while you can read it. `path` shows its
  home-relative path, or `/shared/<name>/...` after a move into a shared folder.
  Other readable locations have no public `path`; the folder remains `ok` and
  the agent still follows it by identity.
- `state` is `ok`, `missing` when the folder was deleted or you can no longer
  read it, or `unset` when no folder is chosen yet (an imported agent whose
  folder was not found here).
- An agent whose folder is `missing` or `unset` does not start, and it never
  falls back to all your files: `POST /api/chats` answers `409` with the type
  `agent_folder_missing`, and so does a message to a chat that is open
  already. A chat whose agent had a folder and was then deleted is refused the
  same way. Choose a folder in the agent's settings, or restore the folder
  from the Trash (it comes back as the same folder, so the agent needs no
  edit), and send the message again.
- A chat with the agent reaches its folders (read only for `read`), its own
  chat folder under `Chats` (attachments and the work folder, read and write),
  the readable shared folders and the skills (read only), and nothing else.
  A folder next to the agent's folders, or above them, is not reachable.
- Scoped access has a limit of **32 folder roots per scope**: the chat folder,
  the agent's folders (at most 8), every skill folder that exists, and the
  shared folders. A chat is never refused for this. The skill folders and the
  agent's folders are always kept. A shared folder that is inside a folder
  already kept is left out, as it is reachable there. Of the others the ones
  with the lowest IDs are kept until the limit is full. The rest are not
  mounted: the model is told how many, and the response of the agent carries
  `folder_notice.not_mounted`, shown in the agent form. Leaving a shared folder
  out only removes access. Each turn checks again, so a change of the shared
  folders applies from the next message. A failure to enumerate shared folders
  refuses access instead of silently omitting them.
- The model sees the agent's first folder at `/workspace/home`, so `/a.txt` and
  `/workspace/home/a.txt` are `a.txt` in that folder. The other folders are at
  `/workspace/folder-<name>`, with " 2", " 3" and so on when two folders have
  the same name. The chat folder is at
  `/workspace/chat`, with the work folder at `/workspace/chat/work/`. The
  shared folders are at `/workspace/shared-<name>`. The file tools, the image
  and document tools, the file search and the system prompt use this layout,
  and a sub-agent of the chat gets the same folders. A file you attach to a
  message is named to the model at `/workspace/chat/<file>`.
- A sub-agent never reaches more than the chat that started it, in folder or
  in mode. A plain sub-agent and a saved agent without a folder work in the
  chat's folder, at the chat's mode. A saved agent with folders can start from
  the chat only when each of its folders is one of the chat's folders or inside
  one; it then works in its own folders at the lower of the two modes. Any other agent is refused
  with "this agent works in another folder than this chat", and `list_agents`
  does not list it.
- Commands of the chat run only in the Sandbox add-on. Its sandbox has the
  agent's folder at `/workspace/home` (read only for `read`), the chat folder
  at `/workspace/chat` (read and write), and nothing else of your files. What
  a command writes anywhere else is not saved, and the command's output says
  so. The built-in `bash` that runs on this machine is never
  offered to such a chat, also when `chats_host_exec` is `on`: a command on
  this machine can read every file LocalAI can read, so nothing can keep it in
  the folder. The Tools menu of the chat says why. On a server without the
  Sandbox, the agent still reads and changes its folder with the file tools.
- Add-ons that a chat with the agent calls work in the same folders. For an
  add-on that works on your files, such as Office or the Sandbox, `/` is the
  agent's folder, and the Sandbox sees the chat folder next to it at
  `/workspace/chat`. This holds for every tool, also one that only reads. See
  [Workspace operations]({{%relref "/addons/contract-reference#workspace-operations" %}}).
- A `PUT` without `folders` or `folder` keeps the folders the agent has, so a
  client that does not know the fields cannot give the agent more files by
  editing it. `"folders": []` clears them. When both are sent, `folders` wins.
- The folder limits what the chat reaches through your files only. The MCP
  servers, web tools and collections that the agent grants are separate
  grants and are not limited to the folder. The chat is not told to build
  [Pages]({{%relref "/features/pages-and-home/pages" %}}) either: pages live in `pages/` of your home,
  outside the folder.
- The folder limits the agent, not you. The Files page, its History, Undo and
  Trash still show all your files, and a chat's file list and attachments
  keep their paths in your home there.
- Triggers and connectors that use the agent keep to its folder too. See
  [Triggers of an agent with a folder](#triggers-of-an-agent-with-a-folder) and
  the [connectors page]({{% relref "/features/chat-apps" %}}#what-a-message-gets).

In the web interface, the **Files** section of the agent form is one choice in
three words:

- **No files**: the agent cannot list, read, write or search any of your files,
  with any tool. It is the Files tool switch (`builtins_off` holds `files`).
  The server then fences the agent's chats to their own chat folder and the
  read-only skill layers, so the image and audio helpers, a Sandbox command and
  an add-on cannot reach your files either. Files you attach to a chat stay in
  that chat. Any folder stored on the agent is ignored while Files is off, and
  choosing **No files** in the form removes the folders from the agent. The
  Look only and Assistant levels already mean **No files** and now select it.
  Watching a folder needs file access: a trigger that watches a folder still
  starts for such an agent but cannot open the files that arrive, and the
  trigger editor says so.
- **All my files**: the Files tool is on and the agent has no folder. A new
  agent starts here, and an agent saved before this choice with Files on and no
  folder reads the same.
- **Only these folders**: the Files tool is on and the agent has a folder list.

The folder rows, the picker and the access of each row show only for **Only
these folders**. The choice shows in the agent's header and page, in the
Effective view (**Cannot reach any of your files**, **Can reach all your files**
or **Can reach only N folders**) and in the Try it list of what a test chat does
not use. The API has no new field: send `builtins_off` with or without `files`
and `folders`.

For **Only these folders**, one row for each folder, with **Choose folder**, which opens a folder
picker of your files (`LocalAI`, `Chats` and shared folders cannot be chosen),
the access of that row, **Can read** or **Can read and change**, and a remove button. **Add folder** stops at eight. A row inside another row shows the mode it works at. The agents list and the
agent's page show the folder and its access, with a lock for read only, or
**Folder missing** with a link to the form. The Files page marks a folder that
an agent uses. A chat with the agent shows the folder next to the agent in the
message box and in the Tools menu. A chat that cannot start because the folder
is missing says so and offers **Edit agent**.

The folder is read from the stores at every request and at the start of every
turn, and nothing is kept in memory between turns (stateless per request), so
every frontend and chat worker of a cluster answers the same. A change of the
folder or the mode applies from the next message of a chat that is open; a
turn that is running finishes with the access it started with.

**Downgrade.** A server older than this feature does not know the folder of an
agent, and one from before several folders reads only the first. If you go back to such a version, its agents work on all your files
again, without a warning, and an agent file of version 2 cannot be imported
there. Before you downgrade, export and delete the agents that have a folder,
or switch off their triggers and connectors.

### Starter agents

`GET /api/chats/agents/templates` lists ready-made definitions (a research
assistant, a code reviewer, a daily digest, a writing editor). They grant
nothing. `POST /api/chats/agents/templates/{id}/create` saves one as your own
(`{"name": "..."}` is optional); then grant it tools with `PUT`.

### Builtin grants

An agent can be given built-in capabilities in its `builtins` list. The list is
empty by default, no template or import turns a grant on, and a chat of the
agent holds only the grants you may use at that moment.
`GET /api/chats/agents/builtin-grants` returns `{grants: [{name, title,
description, sensitive}]}` for the editor. The names are `memory.write`,
`profile.write`, `skills.write`, `pages.write`, `dashboard.write`, `knowledge.write`,
`triggers.propose`, `triggers.run`, `talkback.channels`, `talkback.new_channel`,
`files.upload`, `act.files`, `act.addons`, `act.send`, `act.spend`,
`saved_agents` (the opt-in group of that name; granting it also switches the
group on) and `history.read` (the agent can search and read your earlier chats
with the opt-in group **Past chats**; granting it also switches the group on, an
agent that works in a folder reads only its own chats, and a run that nobody
watches, a messaging chat and a sub-agent never hold it; see
[Past chats]({{%relref "/features/chats-and-agents/chats#past-chats" %}})). A sensitive grant changes what the agent may do or reach later; the
editor marks it. The five `*.write` grants let the agent propose a memory note,
its profile, a skill, a page or a dashboard change with `propose_change`; you
confirm every proposal, and you can undo it. `knowledge.write` (**Add to
knowledge**, sensitive) lets it propose a text for one of its collections; see
[Adding to Knowledge]({{%relref "/features/files-and-documents/collections#let-an-agent-propose-a-text-for-a-collection" %}}). The **Tools** list of the agent form
and of a chat show each grant once, as an option under the switch it refines:
"Create and manage triggers" holds "Also propose from messaging apps" (`triggers.propose`)
and "Let triggers start from messaging apps" (`triggers.run`); "Suggested
changes" holds the five `*.write` grants; "Knowledge" holds `knowledge.write`; "Your files" holds `files.upload` and
`act.files`. An option is greyed, with the reason, while its switch is off, and keeps its
stored value. `talkback.channels` and `talkback.new_channel` are in the section
"Messaging apps", and `act.addons`, `act.send` and `act.spend` in "When nobody is
watching". "Saved agents" is one switch: either the group `saved_agents` in
`builtins_on` or the grant of that name turns it on, switching it off removes both, and
switching it on adds only the group. Each switch and option has one line that says what it
lets the model do, and the form names a messaging connection of the agent that does not
allow an option the agent holds, with a link to the permissions of that connection. A chat
of the agent lists the grants it holds, read only. "Select all" never touches a grant. A write to the agent without the
`builtins` field keeps them, an empty list clears them and an unknown name is
refused (an import drops it).

In a chat that a messaging connection started, the grants also decide which tools
the model gets. A tool exists when the sender holds the capability on the
connection, the agent holds the grant, and the chat did not switch the tool off.
Four grants work this way: `triggers.propose`, `triggers.run`, `talkback.channels`
and `files.upload` (a sender's files going to the agent; it does not let the agent send files out), plus the sensitive
`talkback.new_channel`. An agent that has no `builtins` list at all keeps the
non-sensitive ones, as before. A sensitive grant is never implied: it needs to be
on the list. The moment you grant any built-in, the list is the whole answer, so
add the connector grants you want to keep (granting only `memory.write` to an agent
that proposed triggers from a messaging app turns those off; nothing is deleted
and they work again when you grant `triggers.propose`). The `act.*` grants are
the ceiling of the action policy of unattended runs (see
[Action policy of unattended runs](#action-policy-of-unattended-runs)).

### Export and import

```
curl .../api/chats/agents/<id>/export > reviewer.json
curl -X POST .../api/chats/agents/import -d @reviewer.json
curl -X POST .../api/chats/agents/import -F file=@reviewer.json
```

The file holds the definition only (kind `localai-agent`): no ID, owner or
time. An agent with tool switches (`builtins_off`, `builtins_on`) is version 4, so that a server that does not know them refuses the file instead of giving the agent every tool. The `builtins` list, `memory` and `memory_save` are extra fields of any version; a server that does not know them ignores them, so the agent arrives without the grants, as before and without its own save rule (it then follows the setting of the person); a server that knows `memory_save` but only `always` and `never` refuses a file with `auto` (400). An import makes a new agent: it has the exported `memory` choice and `memory_save` rule and none of the original's notes. An agent without a folder or tool switches is version 1. An agent with one folder is
version 2 and carries `folder` with the folder's path at export time and the
mode, for example `{"path": "/Projects/Acme", "mode": "read"}` (the path is
empty when the folder is missing, not chosen, or now outside your home).
A followed folder in a shared location stays usable by the original agent,
but its export carries an empty path and the mode. Import keeps it scoped
with state `unset` and warns you to choose a folder; it never interprets the
shared path as a folder in your home. An agent with several folders is version 3 and carries `folders`, a list of
the same `{path, mode}` entries. A server that reads only
version 1 refuses a version 2 file, and one that reads only version 2 refuses
a version 3 file, so an agent limited to folders is never
imported somewhere as an agent with more files. On import each path is looked up;
a folder that is not found stays in the list as a row that is not chosen, with
the mode, and a warning names it. An import always makes a
new agent. The folder is looked up by its path in your files; when you have
no such folder, the agent is saved with the mode and no folder (`state:
unset`), it does not start until you choose one, and a warning says so. A name you already use gets
" (2)", " (3)" and so on, and a model that this server does not have, or that you
may not use, is dropped. The response lists such changes under `warnings`. The
grants (MCP servers, add-ons, collections, skills) are kept as names; a grant
that names something you do not have gives the chat nothing, as for any agent.
Secrets are never part of an agent, so the file has none.

### Run history

`GET /api/chats/agents/{id}/runs` lists the chats that ran with the agent,
newest first: the ones you had with it, the ones its triggers started
(`origin: trigger`) and the ones its connectors started (`origin: connector`),
each with `running` and `last_turn`. `total`, `failed` and `running` count every
run among your newest 5,000 chats. Open a run with `GET /api/chats/{chat_id}`.
For an agent shared with you, the runs are your own chats with it.

### Share an agent

You can share a saved agent with other people on the server, or with everyone.
Use **Share** in the row menu of the agent or in the header of its page. The
[Share dialog]({{% relref "/features/sharing" %}}#the-share-dialog) finds people by name and
gives each one a role:

| Role | What the person can do |
|------|------------------------|
| `use` | Chat with the agent in their own chats. They see its name, its description and what it needs. |
| `viewer` | The same, and they read the full definition: instructions, tools and the things it names. |

Only you edit, delete, export or share the agent. The person can not make a
trigger, a task or a connector with it, and your own triggers and connectors
keep running as you.

A person who chats with a shared agent runs it as themselves. The chat, the
memory, the files, the folders, the add-ons, the collections and the connections
are theirs, exactly as for their own agent. Only the definition comes from you.
Your files are never used for them, and no answer to them carries a token, a
secret or the ID of your connection.

The agent never runs with more than the person's own chats start with. With the
role `use` the person does not read your instructions, so what the agent asks for
is narrowed to what their own new chat gets:

- **Network.** Their chat starts with the narrower of the agent's network preset and
  the instance default for a new chat, never with your preset above it. The person
  can widen the network of the chat themselves, up to the instance limit.
- **Opt-in tool groups.** The groups the agent turns on (its saved agents, the
  earlier chats) start off in their chat. The person can turn them on in the chat.
- **Built-in grants.** The agent holds a grant only when the person may use it, as
  for their own agent.

The connections the agent names are yours. A chat of the person uses one of them
only while you share that connection with them (role `use`), and only when it is
not a choice:

- When the person has no connection of their own for the add-on and holds a
  grant on exactly one of the agent's connections for it, the chat uses that one.
- When the person has a connection of their own for the add-on, or holds grants
  on several of the agent's connections for it, the chat uses none of yours by
  itself. The person picks the connection in the chat, from their own and the
  ones shared with them.
- The agent's connections do not lock the person's chat, as they do in your
  own chats: the person may always choose another one.

Sharing the agent does not share what it uses. Before the dialog adds a person,
it lists what the agent names and asks you, for each thing that can be shared,
to choose one of the options the server offers for it, or **Not shared**. The
options come from the roles the kind of the thing grants:

| Thing | Options | Role of the grant |
|-------|---------|-------------------|
| Collection | **View** | `viewer`: search and read. To let the person add entries, give `editor` from the collection's own Share dialog. |
| Connection | **Use** | `use` |
| Add-on resource | **View**, and **Use** when the agent writes it | `viewer`, and `editor` for **Use** |

A folder, a skill, an add-on, an MCP server and a built-in grant can not be
shared this way: the person needs access of their own. The dialog applies each
choice as a grant on that thing, for the same person.

The agent page of a person tells them what they are missing: each thing the agent
names is **ok**, **missing** (not shared with them) or **unavailable** (the server
or their account does not offer it). A connection of yours is **ok** while you
share it with them and **missing** otherwise. The list also shows the network
preset and the opt-in tool groups the agent asks for: **ask** when their chat
starts without it and they can turn it on, **unavailable** when the instance does
not allow it. A chat with the agent shows the same notice before the first message.

When you lower the role or remove the person, a run of the agent in one of their
chats stops, and the chat takes no new message with the agent. Their past chats
stay readable. When you delete the agent, every grant on it is removed.

The API:

| Method and path | What it does |
|-----------------|--------------|
| `GET /api/chats/agents` | Your agents, then the agents shared with you. `access.role` is `owner`, `use` or `viewer`; `access.owner` names the person who shared it; your own agents carry `access.sharedWith` and `access.share_id`. |
| `POST /api/chats/agents/{id}/share` | The ID of your agent in the sharing API, made on the first call. Owner only. |
| `GET /api/chats/agents/{id}/references` | What your agent uses, and how each thing can be shared: `share.kind`, `share.resource_id`, `share.options` and, for each option, the role it grants in `share.roles`. For a collection or a connection that you own, the call makes its record in the sharing API, as `POST /api/collections/{name}/share` and `POST /api/chats/connectors/{id}/share` do; nothing is granted. Owner only. |
| `GET /api/chats/agents/{id}/needs` | What an agent you may use needs, and the state of each thing for you: `ok`, `missing`, `unavailable` or `ask`. Kind `network` is the agent's network preset and kind `tool-group` an opt-in group it turns on. |

Share with the [sharing API]({{% relref "/features/sharing" %}}#api), kind `agent`, the ID from
`POST .../share`, roles `use` and `viewer`:

```bash
curl -X PUT -b session=... \
  -H 'Content-Type: application/json' \
  -H 'X-LocalAI-Confirm: r0123456789abcdef0123456789abcde' \
  -H 'Sec-Fetch-Site: same-origin' -H 'Sec-Fetch-Mode: cors' -H 'Sec-Fetch-Dest: empty' \
  -d '{"role":"use"}' \
  http://localhost:8080/api/shares/agent/r0123456789abcdef0123456789abcde/grants/@everyone
```

Errors: `403` with `authorization_error` for an action that is the owner's
alone; `404` for an agent that does not exist or is not shared with you (the two
are one answer); `409` with `agent_not_shared` for a new message in a chat whose
agent is no longer shared with you; `409` with `sharing_disabled` when the server
does not share agents.

Sharing needs named accounts. In local-owner mode, with no accounts, the agents
show no sharing. The grants live in the sharing policy of the add-on owner (see
[Share with your team]({{% relref "/features/sharing" %}}#distributed-mode)). The agent keeps only
the ID of its sharing record, in the agents table, so every frontend reads the
same one. A frontend that is not the add-on owner asks the owner for every
sharing decision, so a shared agent works on every frontend, also when the
add-on owner is a separate process (`--addons-role=owner`). When the owner
cannot be reached, no shared agent is listed or run: the agents list shows your
own only, a new message in a chat with a shared agent is refused, and a running
turn of a shared agent stops. With add-ons off, no agent is shared.

## Connectors

A [connector]({{% relref "/features/chat-apps" %}}) binds a Telegram bot or a Slack app to an agent, so a person can talk to it from Telegram or Slack. An [MCP connector]({{% relref "/features/chat-apps" %}}#mcp-servers) is a tool server that an agent can name in its `mcp_servers` list.

## Triggers

A trigger starts a chat as you, by itself. Each run is an ordinary chat of
yours: it uses your files, your quota and your model allowlist, you can open it
and undo what it changed, and it is marked with its trigger. To repeat a
prompt inside one chat, every few minutes, use a
[loop]({{%relref "/features/chats-and-agents/chats#loops-repeat-a-prompt-in-a-chat" %}}) instead
(see *Loop or trigger?* there). There are two kinds.

| Kind | Runs | Fields |
|---|---|---|
| `schedule` | At a time | `schedule`, `time_zone` (an IANA name; UTC when empty) |
| `file` | When a file arrives in a folder of your files | `folder` (a path inside your files, such as `/Contracts`), `quiet_seconds` (default 30), `min_gap_seconds` (default 60) |

A schedule is one of `daily 08:00`, `weekdays 09:30`, `weekly mon,thu 09:30`,
`monthly 15 08:00`, `once 2026-12-24 18:00` or five cron fields. The cron fields are numbers, lists, ranges and steps (`*/15`, `1-5`, `1,15`); names such as `mon` or `jan` are not read, and `weekly` takes day names. A time that
does not exist in the time zone (a daylight-saving gap) moves to the first valid
minute after it, and a repeated hour runs once. `GET /api/chats/triggers/preview?when=...&tz=...`
shows the next times and says what the text means.

A file trigger waits until the file has not changed for the quiet period, then
starts one run for that version of the file. The first message of the run says
`A new file arrived: /Contracts/a.txt`. A run's own writes never start the same
trigger. A file that changes again after a run of the trigger began waits for
the trigger's minimum gap (`min_gap_seconds`, 60 by default, at most a day)
before it starts another run, and further changes while it waits start nothing
extra. Files that were already waiting when the run began still run. With the
daily limit, this keeps a writer that reacts to the run's output, such as a
page that saves after each change it sees, from starting runs in a loop: at
most one run a minute and 24 a day by default. An `AGENTS.md` file in a folder, or above it, is added to the first
message as rules for the run. Treat it as text from whoever can write there.

### What a run gets

- **Its message.** The first message is the prompt of the trigger, the folder
  rules and, for a file trigger, the file. LocalAI adds one closing line to every
  trigger run: "When you are done, answer with one short plain sentence about what
  changed. Do not print JSON or code unless the task asks the user for it." The
  answer is what you read in the chat and the inbox, so write the prompt without
  a request for JSON output. A connector chat is a talk with a person and does not
  get the line.
- **Who it is.** The run is a chat of the trigger's owner, with the owner's
  grants. If the owner is over quota, the run fails with that reason and the
  owner gets a notification.
- **Which tools.** With an agent (`agent_id`), the tools of that agent. Without
  one, the run uses the model and the add-on and skill switches of the chat that
  made the trigger (`source_chat_id`), or the defaults of a new chat. An
  unattended run with no agent gets no add-on tool that acts at once, such as the
  Sandbox: name the add-on in an agent to allow it. A run never gets
  `propose_trigger` and cannot change its own trigger.
- **Network.** The Sandbox commands of a run reach the network of the agent's
  `network` field, under the administrator's ceiling, and nothing without an
  agent. Nobody watches a run, so no card asks: a blocked host stays blocked.
  A sub-agent follows the network of the chat that started it, its rules
  included, and never shows a card either.
- **Limits.** A run has 15 minutes by default (`chats_trigger_run_limit`, 1m to
  24h). A trigger can set a shorter or longer time limit of its own with
  `run_limit`, a duration such as `30m` or `2h` from `1m` to `24h`; empty or `0`
  means the administrator's limit. That setting is the ceiling: a trigger whose
  `run_limit` is longer is saved as written but each run is stopped at the
  ceiling, and a lowered ceiling applies to the next run and to the check of a
  run that is found running after a restart. The API shows `run_limit` and `effective_run_limit`, the one
  that applies now. A proposal from a messaging app is held to the same
  ceiling when the trigger is set up by itself. At most 2 runs go at once, one per trigger, and a trigger has a daily
  limit (`daily_cap`, at most 1000 or `chats_trigger_max_daily_cap` when an
  administrator sets it lower). When you set none, a schedule trigger gets one
  run for each time its schedule fires in a day, and at least 24: a trigger
  every 30 minutes gets 48 and one every 5 minutes gets 288. A limit you set
  is used as written. A schedule trigger is switched off, and you get a
  notification, after 3 failed runs in a row. One that runs more often than
  hourly is also switched off after 3 runs in a row that the daily limit
  skipped. A run that is skipped because the previous run is still going is
  recorded once for a stretch of such skips, not once for each missed time. A run that is skipped or fails shows its
  reason on the trigger. An administrator sets the limits of the triggers in the
  [runtime settings]({{%relref "/administration/runtime-settings" %}}).
- **Cost.** A run is a chat turn on the trigger's model, so it uses tokens. A run
  records the prompt and completion tokens of its model requests (`tokens` on the
  run), and a trigger reports the average of its last runs, the most runs one day
  can have, and whether the model is a remote service (`cost` on the trigger).
  LocalAI keeps no price list, so the cost is in tokens only.
- **History.** A trigger keeps its last 200 finished runs, and every run of
  the last two days. A finished run older than 30 days is removed, whatever the
  count. A run that is queued or running is never removed. Set
  `--trigger-run-keep` and `--trigger-run-retention-days` (`-1` keeps all) to
  change this. A file version that ran can run again once its old run
  is removed.
- **When it cannot run.** A deleted agent fails the run with "the agent was
  deleted" and switches the trigger off. A disabled owner skips the run. An owner
  who no longer exists, or a folder that is gone, skips the run and switches the
  trigger off. An agent whose folder is missing or not chosen skips the run with
  "the agent's folder is missing" and switches the trigger off. Each of these
  tells the owner.

### Action policy of unattended runs

A trigger run or an agent job has nobody at the screen to approve a step, so it
starts read only. LocalAI checks every tool call of such a run at the moment of
the call, not in the prompt. A tool that only reads always runs. Any other tool
runs only if the run holds the grant for its class. Add-on tools and MCP tools
always have a class. The other built-in tools either read or need a grant that a
run which started by itself never holds. Nothing is allowed on a doubt: a trigger or a connection
that cannot be read at that moment gives a read only run.

| Class | Grant | Tools |
|---|---|---|
| Read | none | Tools that only read: the add-on tools of kind `read`, the file reading tools, web search and fetch, and the other built-in tools that only read. |
| Files | `act.files` | `write_file`, `edit_file`, `move_file`, `delete_file`, and the built-in `bash` of this machine. |
| Add-on actions | `act.addons`, or `act.addons:<add-on id>` for one add-on | Every add-on tool whose kind is not `read` (`action`, `write`, `schema`, `create`), including an add-on that serves `bash`. |
| Outside | `act.spend` | MCP tools (of a server or a connector), and an add-on action tool that the add-on declares with a permission, which means it acts outside LocalAI with a credential. The last one needs `act.addons` as well. |
| Send | `act.send` | Reserved for messages beyond the delivery target. No tool of this class exists yet. |

What a run holds:

- **A trigger run** holds the `acts` of its trigger, limited to the `act.*`
  grants of its agent (`builtins`). A trigger without an agent has no ceiling but
  its own `acts`. Set `acts` in the body of `POST /api/chats/triggers` or
  `PUT /api/chats/triggers/{id}`: leave it out to keep the value, send `[]` to
  clear it. The trigger form (the **Permissions** section on the Triggers page)
  edits the same list. A model can ask for `acts` only when it proposes a trigger
  (`propose_trigger` takes an optional `acts` list), and only for what its own
  agent holds; no tool changes the `acts` of a trigger afterwards. The proposal
  card lists them, and confirming it grants exactly those. A proposal from a
  chat of a messaging app carries only the acts that the binding allows
  (`approval.acts`, see [Runs may act]({{% relref "/features/chat-apps" %}}#runs-may-act)) and the
  agent holds; the rest is dropped and named in the proposal. A connection starts
  such a trigger without asking only in the `auto` mode, when every act is allowed
  for the binding and the sender is paired; otherwise it is left for a yes. A trigger whose plan names a file it writes (`plan.writes`) also holds
  `act.files`, because you saw that file on the proposal card. Triggers that exist
  before this change keep running, but they now read only unless they set `acts`
  or `plan.writes`.
- **An agent job run** holds the `act.*` grants of its agent, and nothing without
  an agent. A job has no `acts` of its own.
- **Read only runs.** A trigger whose proposal said its runs only read
  (`access: read_only`), or that a connection made without asking while its
  approval mode is `auto_readonly` (see
  [Approval modes]({{% relref "/features/chat-apps" %}}#approval-modes)), holds nothing: every
  tool that is not a read is refused, whatever `acts` and the agent say. A
  connection that cannot be read is treated as `auto_readonly`.
- **Sub-agents.** A sub-agent of an unattended run has the same limits as its
  parent. A run that nobody watches cannot start a saved agent as a sub-agent.

The trigger as REST returns it says what applies: `acts` is the list the trigger
holds, `acts_held` the part of it that applies to a run (within the grants of the
agent, plus `act.files` for a plan that writes a file), `read_only` whether a run
can only read and `read_only_why` why: `no_acts` (it holds none), `agent` (its
agent holds none of them), `declared` (the proposal said `read_only`) or `binding`
(a connection in `auto_readonly` mode made it). A trigger that a connection made
without asking also holds only the acts that its binding allows now, and `binding` gives the ID of that connection (it is left out for other triggers). The Triggers page shows the same:
a "Read only" mark on each row, and the reason on the trigger.

A refused tool stays in the tool list and answers with one plain sentence instead
of running, for example "I did not run write_file: nobody is here to approve it
and this run has no permission for act.files. Say in your answer what you wanted
to do; the owner can allow act.files for this run." The model then reports it in
its answer. The refusal is a result, not an error, so the model does not retry.
Each call of a tool that is not a read writes one log line, `studio: unattended
action`, with the chat, the trigger or job, the tool, `allowed`, `reason`
(`granted`, `not_granted` or `read_only`) and the missing grant.

### Triggers of an agent with a folder

A trigger whose agent has a [folder](#folders-of-an-agent) keeps to that
folder, like every chat of the agent:

- A file trigger must watch the agent's folder or a folder inside it. Saving
  one that watches another folder (at create, at update, and when you confirm
  a proposal) is refused with `400`: "the trigger's folder must be inside the
  agent's folder". A file outside the folder never starts a run.
- Each run checks again. A trigger made before the agent got its folder, or
  whose agent moved to another folder since, skips the run with "the trigger's
  folder is not inside the agent's folder" and is switched off.
- A run whose agent's folder is deleted, unreadable or not chosen never runs on
  your other files: it is skipped with "the agent's folder is missing", no chat
  is made, the trigger is switched off and you get a notification. Saving a
  trigger for such an agent is refused until you choose a folder. Restore the
  folder (or choose another one) and switch the trigger on again.
- If the complete file access exceeds the 32-root limit, the run fails without
  starting a chat. The trigger stays enabled: reduce the available shared
  folders and retry. This differs from a missing agent folder, which switches
  the trigger off.
- The rules of a run come from `AGENTS.md` files in the agent's folder only:
  for a schedule trigger the agent's folder itself, for a file trigger the
  folders from the agent's folder down to the watched folder. An `AGENTS.md`
  above the agent's folder is not read. The first message names folders and
  the file as the agent's chats see them: `# Folder rules: /workspace/home`
  and `A new file arrived: /workspace/home/In/a.txt`.
- The run's chat reaches what a chat of the agent reaches, nothing more.
- In a chat with the agent, the trigger tools name folders as the chat does
  (`/In` and `/workspace/home/In` are `In` in the agent's folder) and refuse a
  folder outside it. `list_triggers`, `update_trigger` and `delete_trigger`
  see only the triggers of that agent; change the others on the Triggers page.

The folder is looked up at every save and every run, so all frontends of a
cluster apply the same rule (stateless per request).

A trigger runs nothing for the time before it was saved or switched on. After
downtime, a missed schedule runs once, not once for each missed time.

### Proposals

The four trigger tools (`propose_trigger`, `list_triggers`, `update_trigger`
and `delete_trigger`) form the **Triggers** group in the chat's Tools menu. They
are on in a chat you start, and one switch turns all four off for that chat
(`PUT /api/chats/{id}/builtins {"off": ["triggers"]}`; see
[the Tools menu]({{% relref "/features/chats-and-agents/chats" %}}#switch-tools-off-for-one-chat)). Switch it off in the Home
composer before you start the chat to have none from the first message.

In a chat, the model can propose a trigger with `propose_trigger` and list yours
with `list_triggers`. A proposal is not active and runs nothing. It waits for you
in the chat and on the Triggers page (`/app/triggers`, `GET /api/chats/triggers/proposals`, with
`?chat={id}` for the ones made in one chat). Change it first if you want
(`PUT .../proposals/{id}` with the same body as a trigger; it stays a proposal),
then confirm it (`POST .../proposals/{id}/confirm`, with your `time_zone` when it
names none) or dismiss it (`DELETE .../proposals/{id}`). The chat carries the
proposal: the tool line of `propose_trigger` has a `card` of kind
`trigger_proposal` whose `id` is the proposal's id, in the live `tool_end` event
and in the transcript, so it shows again after a reload, and a
`trigger_proposed` event announces it to a client that is watching. If a proposal was made in a chat that
has an agent, the trigger keeps that agent. At most 10 proposals wait at a time,
and you can have 50 triggers (`chats_trigger_max_per_user`).

The card sits below the text of the message it belongs to. When you answer it,
it does not go away: it turns into a receipt, and the answer is kept on the
server with the proposal (`GET .../proposals/{id}` returns `state`: `pending`,
`accepted`, `declined` or `undone`), so the receipt shows again after a reload,
in another browser and on every frontend of a cluster.

- **Accepted** shows what was scheduled in plain words ("Scheduled: Every Monday
  at 09:00 (Europe/Rome)"), the name of the trigger, its next run, **View
  trigger** (the trigger has the proposal's id) and **Undo**.
- **Undo** (`POST .../proposals/{id}/undo`) switches the trigger off and removes it
  from the Triggers page. The proposal stays as an undone record, and **Schedule
  it again** (`POST .../proposals/{id}/reopen`) makes it wait for your answer
  again. Undo is refused with `409` once the trigger ran (a run started by hand
  counts) or you edited or switched it on the Triggers page; the receipt then
  tells you to remove it there. The response carries `can_undo` and `undo_blocked`
  (`ran` or `edited`).
- **Declined** leaves a quiet one-line receipt with **Restore**.
- A trigger you delete on the Triggers page ends the receipt: the card says the
  trigger was removed (`404`).

At the start of its next turn the model is told what became of the triggers it
proposed in the chat (accepted, declined or undone), so it does not say a
trigger runs when it was undone, and does not propose a declined one again
unless you ask. A declined or undone proposal does not count towards the limit
of 10 waiting proposals or 50 triggers.

A schedule trigger runs at most once every 5 minutes, however it is made. A
shorter one (for example `* * * * *`, every minute) is refused with the reason,
and the model is told to propose a slower one. An edit of the proposal keeps the
same floor, and so do the Triggers page and the REST API when you create a
trigger or change its schedule. A trigger that was saved with a shorter schedule
before this floor keeps running; you can rename it or edit its prompt, and only
a change of its schedule is checked.

A proposal also carries a plan, so you see what it does before you create it:
what a run reads (`reads`), the file in your home it rewrites (`writes`), the
Home widget that shows that file (`widget`, the widget's `id` in `LocalAI/home.json`)
and about how many tokens one run uses (`estimated_tokens`). A proposal can
also ask for a time limit for each run (`run_limit`, for example `30m`). The
card lists them with when it runs and its first run, that it runs as you with
the chat's model, the cost (and whether the tokens leave the server), the time
limit when there is one (and the lower limit that applies if the administrator
set one) and how to stop it.
When you create a trigger whose plan names a widget, LocalAI writes the
trigger's id into that widget as `trigger`, so the widget can show when it runs.
A form that edits a trigger and does not send the plan keeps it.

### Changes from a chat

The model can also change the triggers you already have. `list_triggers` gives
each trigger a short `ref` (for example `t1a2b3c4d`): it stays the same for the
trigger and it is not the trigger's id. The list shows no prompts. To read the
prompt and the other settings of one trigger, the model passes its `ref` with
`include_prompt: true`. A `ref` of another user's trigger, or one that does not
exist, is an error for the model. The model can also name a trigger by its
exact name when only one trigger has that name.

- `update_trigger {ref, name, prompt, when, time_zone, daily_cap, run_limit, enabled, reads, writes, widget, estimated_tokens}`
  changes the fields it names and keeps the others. The same rules as for a
  proposal apply: a schedule that changes runs at most once every 5 minutes, the
  folder and the `writes` file must be in your files. A change of anything
  other than `enabled` does nothing by itself: a card shows each changed field
  before and after, with **Apply** and **Dismiss**. A change that only pauses
  (`enabled: false`) or resumes (`enabled: true`) the trigger applies at once,
  because you can take it back, and its card is a receipt with **Undo**.
- `delete_trigger {ref}` deletes nothing by itself: a card shows the trigger's
  name, when it runs, whether it is on and its last run, with **Delete** and
  **Keep**.

The tool descriptions tell the model which change waits and which applies at
once, so it does not say a change is made before you apply it. At the start of
its next turn the model is told what you did with the changes it asked for in
the chat.

After you apply a change the card is a receipt with **Undo** for one day.
**Undo** puts the trigger back as it was before the change. For a delete it
restores the trigger with the same id and settings; its run history is not
restored, and the chats of its runs were never removed. **Undo** is refused
(`409`, with the reason) after the day, when the trigger was changed on the
Triggers page since, or, for a delete, when you already have 50 triggers. A
change that waits cannot apply once the trigger was changed or removed after
the model asked for it: the card says so and keeps only **Dismiss**.

The card's change is kept on the server, so the card shows the same state
after a reload, in another browser and on every frontend of a cluster. The
tool line has a `card` of kind `trigger_change` whose `id` is the change's id,
and a `trigger_proposed` event announces it to a client that is watching.

| Route | What it does |
|---|---|
| `GET /api/chats/triggers/changes/{id}` | The change: `op` (`update`, `delete`, `pause` or `resume`), `state` (`pending`, `applied`, `dismissed` or `undone`), `trigger_id`, `before`, `after` (absent for a delete), `fields`, `stale`, `can_undo`, `undo_blocked` (`expired`, `edited`, `removed` or `full`) and `last_run`. `404` a week after it was answered, when it is no longer kept. |
| `POST .../changes/{id}/apply` | Makes the edit or deletes the trigger. `409` when the trigger changed since or the change was answered. |
| `DELETE .../changes/{id}` | Dismisses the change (**Keep** for a delete). The trigger stays as it is. |
| `POST .../changes/{id}/undo` | Takes an applied change back, for one day. |

A change does not count towards the 50 triggers or the 10 waiting proposals. At
most 10 changes wait at a time. Like the other trigger tools, these tools are
offered only in a chat a person started: a trigger run, a connector run and a
sub-agent never get them. A chat that a messaging app started gets `propose_trigger`
and `list_triggers` when the connection holds `triggers.propose` and the agent holds
the grant "Also propose from messaging apps"; `update_trigger` and `delete_trigger` stay in
the app.

### Clusters

In a cluster, one frontend runs the triggers at a time: the one that holds an
advisory lock in the database. A due time or a file version is one unique row,
so it runs once even when two frontends try. If that frontend stops, another
takes over, ends the runs whose chat is over as failed ("interrupted by a
restart") and tells the owner, and goes on. Triggers need the database; the
JSON store is for a single process.

With [chat workers]({{% relref "/administration/distributed-mode" %}}#chat-workers), the frontend that
leads still starts and watches each run, and a chat worker runs its turns.

The same holds for one server. A run that a stop or a restart cuts short is
recorded as interrupted, its chat shows the turn as interrupted (not done, even
if some text was written), and its owner gets a "failed" notification. The
triggers are checked every 5 seconds, so a schedule starts within 5 seconds of
its time and a file trigger within 5 seconds of the end of its quiet time.

### API

```
curl -X POST .../api/chats/triggers -d '{"name": "Morning digest", "prompt": "Summarise yesterday.", "kind": "schedule", "schedule": "weekdays 09:00", "time_zone": "Europe/Rome", "enabled": true, "model": "my-model"}'
curl .../api/chats/triggers
curl -X POST .../api/chats/triggers/<id>/run      # start a run now
curl .../api/chats/triggers/<id>/runs
```

A trigger in a response also has `plan` (`reads`, `writes`, `widget`), `cost`
(`estimated_tokens`, `average_tokens` and `runs`, `max_runs_per_day`, `remote`),
`last_done` (the end of the newest run that ended well), `min_gap_seconds` for a
file trigger, and each run its `tokens`. A create or replace takes `plan`,
`estimated_tokens` and `min_gap_seconds`; an empty one keeps the stored value.

`GET`, `PUT` and `DELETE /api/chats/triggers/{id}` read, replace and delete one,
and `POST .../enable` and `.../disable` switch it. Deleting a trigger stops its
running run and drops its queued runs; the chats it made stay. Another user's
trigger is always `404`. Triggers are your own settings, so they have no
administration tools.

## Inbox

The inbox lists what ended while you were elsewhere: a trigger run that
finished or failed, or something that needs you. Each item has a kind
(`run_ended`, `run_failed` or `needs_attention`), a status (`ok` or `failed`),
a source (`trigger`, `connector` or `system`; empty on items stored before
sources existed), a short title and text, and a link to the chat it is about.
A chat that a run started is unread until you open it: opening the chat marks
its notifications read.

The **Notifications** entry is in the app on every page, not only in the chat
page. On a wide screen it sits in the sidebar, above **Administration**; on a
narrow screen it is an icon of the rail; on a phone it is a bell in the header
(and an entry in the menu). It shows the unread count (99+ above 99), in red
when an unread item is a failure, and opens one panel (a sheet over the bottom
of the screen on a phone). An administrator always sees the entry. Anyone else
sees it once they have items. Nobody sees it when the chats are off or the
account lacks the `chats` feature. A change that waits for you is listed in the
panel but is not in the count. An administrator also sees an **Activity** tab in the panel, with installs, updates and removals (see [Activity]({{% relref "/administration/activity" %}})); operations are never in the count. In the panel:

- **All**, **Unread** and **Failed** filter the list. **Failed** shows every
  failed run and every trigger or connector that was switched off.
- Items are grouped under **Today**, **Yesterday** and **Earlier**. Each one
  shows what sent it, when (the exact time on hover) and, for a failure, the
  reason.
- A click on an item marks it read and opens its chat. A connector that was
  switched off opens the connectors page.
- Hover an item (or focus it) to mark it read or delete it. With the keyboard,
  the arrow keys, Home and End move between items, Enter opens one, Delete
  removes it and Esc closes the panel.
- **Mark all read** is in the panel header; **Clear read** and **Clear all**
  are in its menu.
- A notification that arrives while the panel is open slides in at the top
  and is highlighted for a few seconds.

You keep at most 500 notifications, none older than 90 days. The age limit applies when you read: an older notification is not listed or counted, and it is deleted the next time one is added. The exception is a set item (see below), which no trim removes.

### Items that LocalAI keeps up to date

Most items are written once. LocalAI itself writes a few kinds that it keeps up to date, such as the list of updates that are available. An item like that has a key, and you have at most one for each key.

- A **set item** shows a set of things, each with an id. When the set gets an id it did not show before, the item comes back as a new, unread item at the top. It reaches the browser push and the Telegram chat once, like any new item. When the set only gets smaller, or its text changes, the item is edited in place: it keeps its place, its read state and its hidden state, and nothing is pushed. An empty set removes the item.
- Delete, **Clear read** and **Clear all** do not forget a keyed item: they hide it. It stays hidden while the set is the same or smaller, and it comes back, unread, only for a new id. Marking it read keeps it in the list, and later edits do not make it unread again.
- A **once item** is posted when its key is new and never again, also after you deleted it. LocalAI uses one for each result of an automatic update, so a failure that repeats at the next pass does not fill the inbox.

Some items have a `code` and `params` in the API (`updates.digest`, `updates.result`, `updates.checkFailed`). The app words them in your language from the params. `title` and `body` stay in English: they are the fallback for a client that does not know the code, and the text of browser push and Telegram. The update items are described in [Updates in the inbox]({{% relref "/models/backends#updates-in-the-inbox" %}}).

```
curl ".../api/chats/notifications?unread=1"          # the unread ones, and the unread count
curl .../api/chats/notifications/count
curl -X POST .../api/chats/notifications/read -d '{"all": true}'
curl -X DELETE .../api/chats/notifications -d '{"ids": [12, 13]}'
curl -X DELETE .../api/chats/notifications -d '{"all": true}'                    # empty the inbox
curl -X DELETE .../api/chats/notifications -d '{"all": true, "read_only": true}' # remove only the read ones
curl -N .../api/chats/notifications/events            # new ones as server-sent events
```

**Clear all** asks first, in the
panel, and says how many notifications it removes. **Clear read** keeps the
unread ones. A removed notification cannot be restored. The list answers with
`total`, everything you keep, because it returns at most `limit` items (50 by
default, 500 at most); the panel loads more on request. A delete answers with
the number removed in `deleted` and the unread count left. You can only ever
remove your own notifications.

Events are live only. A client that was away lists the inbox. In a cluster, a
notification added on one frontend reaches the clients connected to any other.
When notifications are read or deleted, the stream sends a
`notifications_changed` event, so your other open tabs read the list again and
their unread badge follows. The notifications are rows in the shared database,
so clearing the inbox on one frontend clears it for all of them. The frontend
that stores a new or revived item runs the push and the Telegram forward, so
each happens once in a cluster. An item that LocalAI writes on its own (see
above) is written by one frontend only: the one that holds the lock of the
work that produces it.

### Browser notifications

The inbox page can ask the browser for permission and subscribe it to push. A
notification is then pushed to that browser, with its title and link and nothing
else, also when no LocalAI tab is open. Clicking it opens the chat.

```
curl .../api/chats/push/key                       # the server's VAPID public key
curl -X POST .../api/chats/push/subscribe -d '{"endpoint": "https://...", "keys": {"p256dh": "...", "auth": "..."}}'
curl -X DELETE .../api/chats/push/subscribe -d '{"endpoint": "https://..."}'
```

- The server makes its VAPID key pair the first time it is needed and keeps it
  with the subscriptions: in the database, or in `push.json` (mode `0600`) under
  the data path. With neither, push is off and these routes answer `409`.
- The endpoint must be an `https` address on the public internet. The server
  refuses private, loopback and link-local addresses, both when a browser
  subscribes and when it sends, and it does not follow redirects.
- You can have 10 subscribed browsers; the oldest go first. A subscription that
  the push service reports gone is removed.
- In a cluster, the frontend that stores a notification sends the push, so it is
  sent once.

## Storage and clusters

Saved agents, triggers, runs, notifications and push subscriptions are stored
beside your chats: in the database when there is one, in JSON files under the
data path otherwise, and in memory when there is no data path (the page then says
persistent features are off, and push is off). The JSON stores are for a single
process.

A cluster needs PostgreSQL, as the chats do. This is how each part of the state
behaves with several frontends:

| State | Mode | How |
|---|---|---|
| Agents, triggers, runs, notifications, push subscriptions | Shared | Rows in the database. Any frontend reads and writes them. |
| What a chat of an agent with a folder may reach | Stateless | The folder and its mode are columns of the agent's row. The frontend or chat worker that runs a turn, a trigger run, a connector reply or an add-on call reads them, and the folder, from the stores each time. Nothing is cached, so an edit applies on every frontend from the next turn. |
| Which frontend runs the triggers | Single runner | The frontend that holds the advisory lock (`KeyChatsTriggers`) runs the Runner. When it stops, another takes the lock. |
| A due time or a file version | Shared | One unique row for each trigger and due key. Two frontends cannot start the same run. |
| Trigger proposals and changes asked for in a chat | Stateless | Rows in the trigger table. Each request reads and writes them in the database, so any frontend can show, apply or undo them. The answer to a change (apply, dismiss, undo) is a conditional update of its row: when two frontends answer the same change at once, one wins and the other answers `409`, so a change is applied or undone once. |
| Quiet periods of waiting files, the memory of a run's own writes | Per leader | Kept by the Runner of the current leader. A new leader reads the change feed again from its saved position, which stays below the oldest waiting file, so a file that was waiting is found again. |
| Live notifications | Shared, live only | Sent over the message bus to every frontend, with the "read or deleted" signal. A client that missed one lists the inbox, which is the record. |
| The supervisor of a running run | Per instance | The frontend that started the run watches it. The leader also ends any run that nobody watches whose chat is over, so a run is not lost when its frontend dies. |
| Where the turn of a run runs | Stateless | On the frontend that started it, or on a chat worker when the frontends send unattended turns there. The turn claim, its heartbeat and the waiting messages are in the database, so either one can run it and only one does. |

### JSON storage

Without an auth database, persistent chats and agent definitions use per-instance
JSON storage. These stores use in-process mutexes and atomic writes. Run one
process per JSON directory on a local filesystem; clusters use the SQL stores.
There is no cross-process directory lock or historical selection ledger.
Status observations use current principal-scoped reads in either backend.
