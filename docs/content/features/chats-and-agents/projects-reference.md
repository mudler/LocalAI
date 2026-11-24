+++
url = "/features/projects-reference/"
disableToc = false
title = "Projects reference"
description = "The pages, memory, tool defaults, lenses, sharing and API of Projects."
weight = 5
devbuild = "feature"
+++

Look up how Projects work in detail: the pages and chips, project memory, tool defaults, the lens model, sharing and the API under `/api/chats/projects`. For what a project is and how you use it, see [Projects]({{% relref "/features/chats-and-agents/projects" %}}).

## Projects

A project groups chats around one folder. It is a folder under `Projects/` in
your own files, so everything in it is an ordinary file that you can open,
edit and undo like any other. A project has:

- a **manifest** (`project.json`) with its name, description and defaults;
- a `files/` folder for the material of the project;
- a `memory/` folder with notes that the chats of the project read. A chat
  changes them only by a proposal that you confirm; you can also edit them
  yourself in the Memory tab or in Files.

The folder name comes from the project name: Latin accents are folded
("Zürich" becomes `zurich`), and a name in another script gets a short id such
as `project-3f9a`. The display name is kept as you typed it.

The list of projects is read from the folders. A project you add, rename or
delete by hand shows up at the next list. Another user's projects are never
visible: their ids answer `404 project not found`.

Projects keep no state in memory: they are stateless per request, and manifest
rewrites are compare-and-set on the file content (a change that lands in
between answers `409`). A per-process lock only narrows the window.

### In the UI

**Projects** in the sidebar opens the list of your projects, sorted by name
(ignoring case), with search, a filter by state and **New project**. A project
page has the tabs Overview, Files, Chats, Agents, Connections, Automations,
Memory and **Tool defaults**: Overview has **Start chat in this project**,
Memory edits the index and the topic files, and Tool defaults edits the
defaults with the same editor as the project form (it has its own **Save**).
The name of a project has at most 120 characters, and the form counts them
near the limit. The first 200 characters of the description are given to chats
as context.

Each default list is a picker fed from what is installed: skills, add-ons (shown
by title), MCP servers of your models, your MCP connectors (shown by name) and
collections. You can also type a name that is not installed. An entry that
nothing installed matches stays in the list, marked **Not available** (or
**Off now** for a connector that is switched off), because it does nothing until
it exists. An empty list means no limit. A non-empty list does two things: new
chats in the project start with only those entries on, and every agent of the
project can use only those entries.
The defaults apply to every new chat in the project: skills, add-ons, MCP
servers, connectors (only the listed ones are on) and
collections (the chat can use only the listed knowledge collections). When a
chat is restricted to collections, its chat
settings show them in a read-only **Collections** section.
#### Attached tools

A project also has an **attached** block, with the same five lists. Defaults
are a limit; attached tools are what the project holds. Every chat of the
project, old or new, and every agent of the project has the attached tools on,
with no per-chat step, and the manifest is read on each turn (a change made
through this server is seen at once; a change by another frontend within a few
seconds): attach a tool and the next turn of a running chat or agent has it.
An attached collection is searchable in the project's chats and agents. This
holds for a turn that a trigger or a loop starts too. Attached means always on:
a chat cannot switch an attached tool off, and a chat that started with such a
tool switched off has it on while it is attached. To turn it off, detach it
from the project. The switch a chat stored is kept, so the tool is off again
after a detach if the chat had switched it off. A trigger, a fork or a
sub-agent copies what the chat itself chose, never the attached tools.

The effective tools of an agent are what it has plus what is attached. A
non-empty limit then removes everything outside it, for chats and agents alike,
and also when the manifest was edited by hand: an attached entry outside a
non-empty limit is ignored on read. A write that attaches a tool outside a
non-empty limit is refused with `400`, and the message names the tool and the
limit. Narrowing the limit under an attached tool is refused the same way. A
write that attaches an empty entry, an entry with white space at its ends or a
control character, or an entry longer than 256 bytes is refused too, and so is
a `connector:<id>` entry in `mcp_servers` (list the connector under
`connectors`). Each list has at most 64 entries.

Attached tools are the owner's. An attached connector, add-on or MCP server can
hold a credential, so it runs only for turns that act as the owner of the
project. A person the project, or one of its agents, is shared with never gets
them. A chat that starts from a messaging connection (a bot) is not the owner
at the keyboard: people outside the app write to it. It gets only the attached
skills and collections. The attached connectors, add-ons and MCP servers reach
such a chat only when the agent of the chat lists them itself. The same holds
for a sub-agent of such a chat. Set the tools with `attached` in
`PATCH /api/chats/projects/{id}`, or in the `attached` key of `project.json`.

`project.json` is a file of the project folder. When another person may write
the project folder (a share with write access), the `attached` block in it is
ignored, as the project skills are, and the project page says so
(`attached_blocked` on a read of the project). The model of an owner chat can
write any file of your home, `project.json` included: the project page is the
place to attach a connector, add-on or MCP server, and the file tools are not a
guarded path. Everyone who can read the project folder sees the names in
`attached`.


An archived project shows a banner with **Restore**; **Edit** and the tool
defaults are read-only until then. **Delete** asks first and says how many
agents, triggers and chats depend on the project; the folder goes to the Trash.

A project whose folder or manifest is damaged shows as **broken**, with the
reason, and has no page. Its row offers **Open in Files**, which opens
`Projects/<slug>`, and **Move to Trash**. Move to Trash puts the whole folder,
files included, in the Trash; restore it from the Trash in Files. It is not
just a removal of the row.

On Home, a **Project** chip sits next to the model and Tools. It lists your
active projects with their chat counts, and **No project** is the default. The
pick belongs to the chat you are about to start. It is forgotten with the draft
and is sent as `project_id` when the chat is created.

An open chat shows its home project as a chip in the header, and a chip with a
lock and **Pulled in** for each project it pulls in. Chat settings have a
**Project** section to change the home project (or take the chat out of its
project) and to add or remove pulled projects, up to eight. A change applies
from the next message, and the server refuses it while a turn is running. On
**All chats**, a Project filter narrows the list, and a chat in a project
shows the project's name on its row.

The lock is not a protection. The files and memory of a pulled project are
read-only at the pulled path, and the chat can still reach the same files
through your home folder (see "Pull in other projects" below).

A saved agent that belongs to a project reads the memory of that project: the
index is in its prompt, and the notes are read only for it. To let it write,
give it the grant **Remember things in the project** (`project.memory.write`).
The agent can then propose a note for the memory of its own project, and you
still confirm each note before it is saved. It never writes the memory of
another project. You can give the grant to any saved agent, but it matters only
for an agent that was created from a project. In a messaging chat it applies
to paired senders only. The grant does not apply to a sub-agent or to a run
that starts by itself, and it has no effect when the agent keeps no notes of
its own (the choice **None**) or when saving notes is turned off.

### The API

All routes are under `/api/chats`, need the `chats` feature, and answer
`409 projects_off` when projects are not available on the server.

| Route | What it does |
|---|---|
| `GET /projects` | List your projects. `chat_count` counts up to 500 chats. |
| `POST /projects` | Create one: `{name, description, defaults, attached}`. `defaults` and `attached` have five lists each: `skills`, `addons`, `connectors`, `mcp_servers` and `collections`. Answers `201` with the `slug` of the folder. |
| `GET /projects/{id}` | Read one. |
| `PATCH /projects/{id}` | Change `name`, `description`, `defaults` or `attached`. A field that is left out keeps its value. |
| `DELETE /projects/{id}` | Delete the folder as one change set that one undo restores. Chats of the project stay and read as having no project. |
| `POST /projects/{id}/archive`, `POST /projects/{id}/restore` | Archive a project, or bring it back. An archived project takes no new chats. |
| `GET /projects/{id}/memory` | The text of `index.md` and the list of topic files. |
| `PUT /projects/{id}/memory/{file}` | Write one memory file: `{content}`. |
| `GET /projects/{id}/chats` | Your chats in the project, newest first (`limit`, 50 by default, at most 200). |
| `PUT /{id}/project` | Put a chat in a project: `{project_id}`. An empty id takes it out. The chat must be idle. |
| `PUT /{id}/projects` | Set the other projects the chat reads from: `{project_ids}`. |

A new chat can start in a project with `project_id` in `POST /api/chats`.

#### Act for another user

An administrator can add `?user_id=<user id>` to any of these routes, and to the
lists of agents, connections, triggers and loops of a project, to act on the
projects of that user instead of their own. This is the same parameter the
other per-user routes use. Every project in an answer has an `owner` field with
the id of its owner.

- A user who is not an administrator gets `403` when `user_id` names someone
  else. Their own id is accepted. The other per-user routes ignore a `user_id`
  from a user who is not an administrator and use their own account. The
  project routes refuse it, so a client that creates projects for a user never
  creates them under the wrong account without notice.
- `user_id` must be a user id: letters, digits, `-` and `_`, at most 36
  characters. Anything else gives `400`.
- An id that names no user of the server gives `404`.
- Each request that acts for another user writes one `User impersonation` log
  line with the caller, the target user and the route. The body is not logged.

### Project memory

The memory of a project is the folder `memory/`. `index.md` is the index, and
the chats of the project read it at each turn. The other files are topic files.
A file name is lowercase letters, digits, dot, dash and underscore, ends in
`.md` and has at most 64 characters. A model that works in a chat of the
project proposes a note, and you confirm it as for any other memory. A project
note must name its file (`target`), such as `decisions.md`; there is no default.
A proposal replaces the whole file, so a note for `index.md` replaces the whole
index. When you
write a file yourself with `PUT .../memory/{file}`, the write is your own edit:
it is made as you, as one change set, and it is limited to 16 KiB, the same as
a note the model proposes. The edit is also accepted for an archived project.

### Tool defaults

The defaults of a project (`skills`, `addons`, `connectors`, `mcp_servers`,
`collections`) apply to a **new** chat in the project. They do not change a
chat that exists. For skills, add-ons, MCP servers and connectors (your own
MCP connectors):

- an empty list, or no list, means **no override**: the chat starts with the
  switches it would have had;
- a non-empty list means **only those are on**: every other skill, add-on or
  MCP server or connector that you could switch is off for the chat (only
  enabled connectors count);
- a name that is not installed or not available is skipped, and a list that
  names nothing that is available is no override;
- a switch that the request to start the chat set itself wins over the
  default. MCP servers and connectors are separate kinds: a request that
  switches off only a connector keeps the MCP server default, and the reverse.
- the connectors that are off are those enabled when the chat starts, so a
  connector you create after that is on in that chat.

Collections are applied as a per-chat restriction. A non-empty list that names
at least one collection you have restricts the new chat to the listed
collections you have: the chat can list and search only those, and a
collection outside the list answers "collection not found", like one that does
not exist. A name you have no collection of is skipped, and a list with none of
your collections is no restriction. A request to start the chat that names
collections itself (`collections` in `POST /api/chats`) wins over the default.
The restriction is set when the chat is created and cannot be changed later. Its
sub-agents and forks keep it. A list from the request that names only collections
you do not have leaves the chat with no collections at all; it does not fall
back to all of them. The restriction stacks with a saved agent: the chat sees
the collections that are on both lists.

### Project skills and attached tools

Attached tools are always on (see Attached tools under Tool defaults). A chat
cannot switch an attached tool off, and a switch-off in a request to start a
chat does not outlast the start of the chat. To turn a tool off for the
project, remove it from `attached`. Defaults are the limit and attached is what
the project holds: a non-empty limit never removes a project skill.

**Project skills.** The skills of a project are files in
`Projects/<slug>/skills/<name>/SKILL.md`. They are on in every chat and agent of
the project and in no other chat. Project skills are above your personal skills
and the skills of an agent. A skill in the working folder of the chat still wins
over them. The skills limit does not apply to them. Edit them in the Skills
section of the project page, or as files, or with the skill routes with
`layer=project` and `project=<id>`.

A project skill is read only while you are the only person who can write the
folder. If someone else has write access to the project folder, project skills
and the attached tools of the project are off and the project page says so.
A project skill cannot be switched off in a chat either. The folder walk for folder skills skips
`Projects/`, so a skill placed there never loads in a chat of another project.

**Moving from `.skills`.** Skills in
`Projects/<slug>/.skills` don't load in any chat. On first start, LocalAI copies each skill from `.skills/` to `skills/` in the
same project, once per project: when `skills/` exists, the project is left
alone, so a skill you delete there does not come back. It does not delete the
source. Remove the old folder yourself when you no longer need it.

### The lens model

A chat has one **home project**. The home project is the lens of the chat: its
`files/` folder is mounted read and write at `/workspace/project`, its
`memory/` folder is mounted **read only** at `/workspace/project-memory`, its
memory index is part of what the model reads at each turn, and the notes that
the model proposes go to its memory. The model does not write to the memory
folder itself: it proposes a note, and you confirm it. A note with no text is
refused. The prompt also names the project and gives the first 200 characters
of its description, on one line.

The same caveat as for a pulled project applies: the home mount of an ordinary
chat is read and write, so the chat can still reach the memory folder through
`/workspace/home/Projects/<slug>/memory`. The read-only mount keeps the normal
path honest; it is not confinement.

A chat of a saved agent that has a folder works only in that folder. In this
release it has no project at all: it cannot start in one, be moved into one or
pull one, and the Home project chip is disabled while such an agent is picked.
A fork of a chat stays in the project of the original and reads the same
pulled projects. A sub-agent mounts the project of its parent chat, read and
write for the home project and read only for pulled ones.

### Pull in other projects

A chat can also **pull in** up to eight other projects with
`PUT /{id}/projects`. A pulled project is an active project of yours that is
not the home project. The chat can read its files at `/workspace/pulled-<slug>` and its
memory at `/workspace/pulled-<slug>-memory`. Its notes are not put in the prompt, and no note is ever
written to it.

A pulled project that is archived or broken is not mounted, and the chat says
so (**Archived, not mounted** or **Unavailable**) instead of showing the
read-only note.

A pulled project is read-only **only at its `pulled-*` mount path**. Projects
live in your home, and an ordinary chat mounts your home read and write. A
chat can therefore still change the files of a pulled project through its path
in your home. Do not treat a pulled project as protected. Real confinement,
where a chat sees only the folders of its project, is in force for agents that
are created from a project: see [Agents created from a project](#agents-created-from-a-project).

### Agents created from a project

A saved agent can belong to a project: send `project_id` when you save it. A
project agent works inside a fence.

- **The fence.** It can use only the `files/` and `memory/` folders of its
  project and its own chat folder. It never sees the rest of your files. Like
  every fenced agent, it also keeps read access to the skill layers and to the
  folders other people share with you; the fence does not cover those. It
  reads the project memory; the `memory/` folder is always read-only for it.
  The `projects` tool of the client can only list, get and show agents,
  connections, triggers and loops in its chat: it cannot create, change,
  archive or delete a project.
- **The ceiling.** When the project lists skills, add-ons, MCP servers,
  connectors or collections in its defaults, those lists cap the agent: it gets
  only the entries that are in both its own list and the project list. An empty
  project list sets no ceiling. The project does not cap the built-in tool
  groups (`builtins_off` and `builtins_on`), the skills that the agent itself
  owns (its own skills layer), the skills that a memory provider offers, the
  add-on connections, the resources and the grants of the agent. Each of these
  stays as the agent's own settings say.
- **Availability.** The agent stops while its project is archived, deleted or
  broken, or when its folders changed: new chats and runs are refused with
  `project unavailable`. Nothing is switched off or deleted. It works again
  after you restore the project, or save the agent again to reconnect its
  folders. A loop of the agent that waits for the model, and a wait that wakes
  the model, check again after a pause that starts at one minute and doubles up
  to 30 minutes while the project stays unavailable; a loop with an interval
  keeps its slots. None of this counts as a failed run.
- **Memory.** Writing the project memory needs the grant
  `project.memory.write`, which is off by default. A note still goes through a
  proposal that you confirm.
- **No sharing.** A project agent cannot be shared; `POST
  /api/chats/agents/{id}/share` answers `400`.
- **Choosing the project.** The new-agent wizard and the agent editor have a
  Project field: one of your active projects, a new project made on the spot, or
  no project. `project_id` on `POST /api/chats/agents` does the same from the
  API.
- **Moving an agent.** `PUT /api/chats/agents/{id}/project` moves an agent into
  a project, to another project, or out of its project. A normal `PUT
  /api/chats/agents/{id}` never does it: it answers `400` for a changed
  `project_id`. The body is `{"project_id": "<id>"}`, with `""` to leave a
  project. Options: `dry_run` (report only, nothing is written), `files_mode`
  (`read` or `write`, the default, for the files folder of the new project),
  `folders` (required when leaving a project, in the form of the agent
  `folders`) or `whole_home` (the agent then works on all your files), `revoke_shares`
  and `disable_triggers`. The answer lists what the move touches: `chats` and
  `loops` that stop, `triggers_outside`, `shares`, `proposals` that are voided,
  `dropped_tools` that the limits of the new project take away,
  `attached_new` (what the new project attaches: the agent gets it at each
  run), `attached_old` (what the old project attached and the new one does not:
  it stops applying), `project_skills_switch` (true when the skills of the old
  project stop and those of the new project start), `memory_grant_dropped` and
  `blockers`. A dry run shows all of it. The move is refused with `409`
  (`agent_move_blocked`) while a chat of the agent runs a turn, while a shared
  agent would enter a project, or while an enabled file trigger watches a folder
  outside the new folders; `revoke_shares` and `disable_triggers` lift the last
  two. On a move the agent gets the two folders of the new project, loses the
  grant `project.memory.write`, and its waiting notes for a project memory are
  voided. Its own notes start empty: the old ones move to `Memory/Moved agent
  notes` in your files and are not deleted. Chats the agent already had keep their
  history but take no new turn (`409`, `agent_moved`: start a new chat), and
  their loops end. A messaging sender gets a new chat on the next message.
- **An agent whose project was deleted.** The agent page shows one banner, with
  a **Detach from project** action: a move out of the project to folders you
  choose.
- **The editor.** The agent editor offers only what the project allows, and a
  new agent starts empty: creating an agent from a project does not copy the
  defaults of the project into it, so the agent holds only what you pick.
- **Export and import.** See below.

`GET /api/chats/projects/{id}/agents` lists the agents of a project, sorted by
name: id, name, description, model, `files_mode`, `memory_write` and
`unavailable`. The project list and the project itself carry `agent_count`. The
`projects` client tool reads the same list with the action `agents`.

#### In the UI

- **The Agents tab.** The page of a project has an Agents tab. It lists the
  agents of the project with a Ready or Unavailable chip, how each one uses the
  files (can edit or read only) and whether it may remember things in the
  project. An agent that cannot start shows why in one line: the project is
  archived or deleted (restore the project), or its folders changed.
- **New agent.** The New agent button opens the agent editor with the project
  chosen. The Files section has two fixed rows and no folder picker: the
  project files, with a Can edit or Read only choice, and the project memory,
  which is always read only. The tool lists of the project, when it has some,
  limit what the editor offers, and the editor says "Limited by the project".
  A project agent shows a Project chip in its header and in the Agents list,
  and Share is replaced by a note: project agents cannot be shared. Export
  stays.
- **Reconnect.** When the folders of the project were replaced, the agent is
  paused and shows a Reconnect button, on the Agents tab and on the agent page.
  It saves the agent again so that it uses the two project folders: the files
  folder at the mode the agent had (read only stays read only) and the memory
  folder, read only. This also works when a folder was renamed or moved away
  and a new one made. A folder that is not one of the two project folders is
  dropped, never kept. Chat stays off until then.
- **Import.** When you import an agent file that belongs to a project, the
  Agents page asks which one: pick one of your active projects, or create a
  project named like the one in the file. The choice is sent with the same
  file as the query parameter `project_id` or `create_project=true`.

#### Export and import

An agent made in a project exports as a version 5 file. The file holds the
agent's definition, the name of its project as a hint, and the two folders of
the agent as the project-relative paths `files` and `memory`. It holds no
folder ID and no path of your files. A server that reads only versions up to 4
refuses the file instead of importing the agent with the wrong access.

An import never takes the project from the file. When you import a version 5
file, the import asks you to pick one of your own projects or to create a
project from the name in the file. The agent then becomes an agent of that
project, with the files and memory folders of that project. The file's `files`
entry only sets the files mode (read or write, read when no entry says write);
the memory folder is always read. The tool lists and the grant
`project.memory.write` of the agent are kept.

The choice is part of the request, never of the file. Send `project_id` (one of
your own active projects) or `create_project=true` as **query parameters** (or
as form fields of a multipart upload); a `project_id` or `create_project` inside
the file is ignored. Until you do, the answer is `409` with the error type
`project_required` and the project name in `project_name`. An archived project
is refused, and so is another person's project (`404`). A client such as the
import dialog must send the choice in the query string. When the agent is
refused, no project is created for it.

### Sharing a project

You share a project with the **Share** action on its folder in Files. This is
the ordinary folder sharing: you pick a person (or everyone) and **read only**
or **read and write**. There is no other way to share a project. The `projects`
tool has no action that shares, unshares or edits a share, and the project
routes have none either.

**In the UI.** Open the project and select **Share** in the header. The
dialog lists the people who have access, lets you add a person or everyone with
**Can read** or **Can edit**, and says that the share covers the files, the
memory and the settings. The header shows **Shared with N** once it is shared.
The button is hidden when accounts are off. A person you share with finds the
project under **Shared with me** in the project list. Its page shows who shared
it and whether they can edit files, has an **Overview** and a read-only
**Memory** tab, and **Pull into a chat** starts a new chat or adds it to a
recent one. A note that another person proposes shows in your proposals as
"<name> proposed a note for <project>". If that person lost edit access before
you confirm, the card says it was not applied.

**What the share covers.** The whole project folder: the files, the memory and
the manifest (`project.json`). The person can read all three, and with a write
grant they can change files. Treat the memory and the manifest as visible.

**What the person can do.**

- See the list of projects shared with them, `GET /api/chats/projects/shared`,
  shown in the UI under **Shared with me** in the project list, with the name,
  the owner's display name and whether it is read only or read and write.
- Pull it into a chat with `PUT /api/chats/{id}/projects`, using its `ref`.
  The chat mounts the files with the mode of the share.
- Read its memory. With a write grant, they can also propose a note from a
  chat. The note waits in **your** queue, shows who proposed it, and is written
  only when you confirm it. With a read grant nothing can be proposed. A change
  proposal has a `proposer` field, a widening of the change proposal contract:
  see the [contract reference]({{% relref "/addons/contract-reference" %}}).

**What the person cannot do.** Make it their home project, create agents in
it, or see it from an agent that works inside a fence (see
[Projects shared with you, fences and triggers](#projects-shared-with-you-fences-and-triggers)
for the fences and for the file triggers). Changes that another person makes inside your
projects do not start your triggers. Changes outside your projects can, and a
note that you confirm is written as you.

**The folder is also an ordinary shared folder.** The same folder shows in the
shared-folder lists, and an ordinary chat of a person with a write grant mounts
it. That chat can write the project memory files directly through that mount,
without a proposal. Give a write grant only to people you trust with the
memory.

**Revoking.** A revoke or a lower mode takes effect on the next turn of a chat
and on the next call of the API. A chat that has the project pulled stops
mounting it. If you delete the project, it leaves the list at once. If you
restore it from the Trash, the share is back, because it is a grant on the
folder.

**The API.** All three routes are read only and belong to the person who asks.
Another person's project that is not shared with them, and an unknown `ref`,
give the same `404`.

| Route | Answer |
|---|---|
| `GET /api/chats/projects/shared` | The projects shared with you: `ref`, `name`, `description`, `status` (`active`, `archived` or `broken`), `mode` (`read` or `write`), `owner_name` and `error`. An archived or a broken project is listed but is not available. |
| `GET /api/chats/projects/shared/{ref}` | One of them. `ref` looks like `s:12`; send the colon as is or as `%3A`. |
| `GET /api/chats/projects/shared/{ref}/memory` | The memory index and the names of the topic files, read through your own grant. An archived or broken project gives `404`. |

The `ref` is the only identity of a shared project. The answers never show the
owner's account, a path, a manifest id or the owner's tool lists. The
`projects` tool has a read only action `shared` that lists the same rows. It
works in a chat that has no fence and for API and MCP callers, and it is
refused in a chat that works inside a fence and in a run that starts by itself,
so that these never learn who shares what.

### Projects shared with you, fences and triggers

A project that another person shares with you never appears in a fence. A
saved agent with a folder (an agent created from a project, an agent with a
folder, and the sub-agents of both) reaches only its own folders, its chat
folder, the skills and the ordinary shared folders. The shared project is left
out, and so are a folder inside it that was shared on its own, and a shared
Projects or home folder of another person, which would reach it. A chat that
has no folder keeps the shared project as an ordinary mount.

A person who has a write grant on a shared project can write into its `files`
folder. LocalAI records the account that made each change. A change by another
person inside one of your projects does not start your file triggers, whichever
folder the trigger watches: only a change by you, or by a chat of yours, starts
a run. A file of yours that another person changes while it waits for its quiet
period is dropped, and starts no run until you save it again. This rule decides
only what starts a run. A run that a trigger starts reads the files as they are
then, including a file that another person has written, so it does not protect
the content that a run reads. A trigger that watches a folder outside your
projects starts for every author.

### Connections, triggers and loops of a project

Three read-only lists show what is connected to the agents of a project. Each
needs your own project (`404` for another person's or an unknown one) and works
for an archived project too. Each also needs the feature of the objects it
lists, as well as the `chats` feature of the project page: `agents` for
`/agents`, `connectors` for `/connections` and `triggers` for `/triggers`.
Without it the route answers `403` and names the missing feature. `/loops`
needs `chats` only, because a loop is part of a chat.

- `GET /api/chats/projects/{id}/connections` lists your messaging connections
  that are bound to an agent of the project: id, kind, name, the agent and
  whether the connection is on. A token or a list of senders never appears.
- `GET /api/chats/projects/{id}/triggers` lists your triggers that run an agent
  of the project: name, kind (`schedule`, `file` or `event`), schedule or folder (a path in your files, such as
  `/Projects/acme/files/inbox`; an event trigger has neither, and its sender, type and matches are
  not in this list), the agent, whether it is on, and `next`, the
  next run of an enabled schedule trigger. The list shows your
  triggers of the agents of the project; `proposed` is always false for now.
- `GET /api/chats/projects/{id}/loops` lists your loops in chats that use an
  agent of the project: the chat, the state and the prompt, cut to 200
  characters. A loop that has ended is history and is not listed.

Each route reads only its own list, so a client that wants all three makes
three calls.

A connection, a trigger and a loop belong to the project through its agents:
a messaging connection is bound to a project agent, a trigger names one, and a
loop runs in a chat that uses one. While a project is archived its agents do
not start. The triggers and the loops skip their runs, one run at a time, and
stay as they are: `enabled` and the loop state do not change. After you restore
the project they run again.

#### In the UI

The project page has a **Connections** tab and an **Automations** tab.

- **Connections** lists the messaging connections of the project with their kind,
  the agent that answers and whether each is on. **Open** goes to the page of
  that connection, where you change it. **Connect** lists the kinds of messaging
  app that are installed. Choosing one opens its setup page with the project
  agent already chosen. If the project has several agents, a small step first
  asks which one answers. A project with no agent shows **New agent** instead,
  because a project agent must answer the messages. An archived project cannot
  add connections: **Connect** is off until you restore it.
- **Automations** lists the triggers (schedule, folder or **Event** for an event trigger, the agent, the next
  run) and the loops of the project. Each trigger opens its own page and each
  loop opens its chat. **New trigger** opens the trigger form with the first
  agent of the project that can work already chosen, and, for a file trigger,
  the `files` folder of the project as the folder. It is off while the project
  is archived and when the project has no agent. If your account has no
  `triggers` feature, the tab says so in place of the list. While the project is archived, a trigger or loop that was
  active shows **Paused** and resumes after you restore the project. One that
  you had switched off stays **Off**. The tab offers **Restore project**.

On the **Triggers** page and on the page of a trigger, a trigger of a project
agent shows the project as a chip that opens the project, the agent list of
the trigger form marks the agents of projects, and a project filter narrows the
list of triggers and its history. In the form, the folder of a file trigger of a
project agent is a choice among the folders of the agent (the project `files`
folder first) and an optional folder inside it. A folder outside them is
refused before **Create**.

A file trigger of a project agent can watch a folder inside the `files` folder
of its project, or the project `memory` folder, which the agent can read. It
cannot watch a folder outside the project, in another project or in your home;
the trigger is refused when you create it or change its folder or its agent.
You cannot create a trigger for an archived project, or switch one on while the
project is archived. A trigger you switched off stays off through archive and
restore. A trigger that is on resumes by itself after the restore.

A connection can start with any of your agents, and a project agent makes it a
project connection. After that it can only move to another agent of the same
project, and a connection outside projects cannot move to a project agent. If
the agent of a connection was deleted, you can point the connection at any of
your agents again. A
message to an archived project is answered with a short note and starts no
chat.

The `projects` client tool reads the same lists with the actions
`connections`, `triggers` and `loops`; the read-only server keeps them. In a
chat that works inside a fence (a project agent, or an agent with a folder) and
in a run that starts by itself, the actions `agents`, `connections`, `triggers`
and `loops` answer for the project of that chat only; another project is
refused. `list` and `get` still show the names and descriptions of your
projects.
