+++
url = "/features/chats-commands/"
disableToc = false
title = "Running commands in a chat"
description = "Let a chat run commands and code in the Sandbox add-on or on the server, with jobs, network rules and limits."
weight = 15
devbuild = "feature"
+++

Let a chat run commands and code with the `bash` tool. The Sandbox add-on runs them isolated when it is installed. Without it, an administrator can allow commands on the server itself. This page says which one serves a chat, how background jobs and network access work, and what an administrator can restrict.

## Running commands

A chat runs commands and code with one tool, `bash`. Who serves it depends on
the server:

- **The Sandbox.** With the [Sandbox
  add-on]({{% relref "/addons/catalogue/work/sandbox-settings#sandbox" %}}) installed, running and on
  for the chat, `bash` runs in an isolated sandbox. The add-on's `bash`
  replaces the built-in one (see [Tools that replace a built-in
  tool]({{% relref "/addons/contract-reference/owner-forwarding#tools-that-replace-a-built-in-tool" %}})),
  so the chat never has two.
- **This machine.** Otherwise, when an administrator allows it, the built-in
  `bash` runs commands directly on the machine that runs LocalAI, **with no
  isolation**. This covers a server with no Sandbox, a chat that switched the
  Sandbox off, and a Sandbox that is stopped or cannot run.
- **Nothing.** With neither, nothing in the chat runs code.

A chat whose agent works in [one
folder]({{%relref "/features/chats-and-agents/chats-agents#folders-of-an-agent" %}}) runs
commands only in the Sandbox, never on this machine. Its sandbox has the
agent's folder at `/workspace/home` and the chat's own folder at
`/workspace/chat`.

At every turn the model is told in one short block which of the three holds.
With host commands the block says that `bash` runs on this machine with no
isolation and with its network, and tells the model to be careful: no
destructive or data-sending commands outside the task unless you asked. When
nothing runs, the block says that the model must not say or imply that it ran
something, nor send a sub-agent to run it; it writes the result as a file
(text, CSV, Markdown, HTML, JSON) or gives you the script, and tells you how to
get commands.

A skill can say that it needs commands with `requires: [exec]` in its
frontmatter (see [Skills]({{% relref "/features/chats-and-agents/chats-skills#skills" %}})). In a chat that cannot run commands, such a
skill is listed to the model as "unavailable here: needs command execution",
without the path to its procedure, so the model explains instead of starting.

### Background jobs

A command that runs for minutes or never ends (a build, tests, a dev server, a
download) does not fit in one `bash` call, which stops after at most 30
minutes. The model starts it with the `job` tool and keeps working. `job` is
one tool with an `action`:

| Action | What it does |
|---|---|
| `start` | Runs `command` with bash in the background, where `bash` runs. It returns a job id such as `job-4k2x7q` and shows the command to you. `label` is a short name for the list: one line of plain text, at most 80 characters. |
| `list` | One line per job of the chat, running first: state, run time, time since the last output, bytes. It never shows the command. |
| `read` | Returns the output since `since` (a byte offset from the start of the job; default: where the last read ended), at most `max_bytes` (default 16 KiB, at most 64 KiB). The text says where the next read starts. |
| `input` | Writes up to 2000 bytes to the job's standard input; `close_input` closes it. |
| `kill` | Sends `term` (default), `int` or `kill`. A `kill` follows after 10 seconds. |

`job` is offered exactly when the chat has `bash`, from the same provider, and
the same switch (the shell group of the chat's tools) turns both off. A chat
whose `bash` the Sandbox serves never runs a job on this machine. In mode `ask`
for [commands on this machine](#commands-on-this-machine), `start` shows the
same card as `bash`, with the whole command, and the approval of the start
covers the input of that job: no card per input. The card says so under the
command, and so does the answer of `start`. Input is still refused when
the chat could not run a command now, and every start, input and kill of a job
on this machine is written to the audit of the commands. When the tools of the
chat cannot be read, or the add-on that serves its `bash` cannot work, `start`
says so and runs nothing. A run that nobody watches (a trigger or an agent job)
needs, for `start` and `input`, what its `bash` needs: on this machine
`act.files` and `act.spend`, in the Sandbox the act grant of the add-on.

A job has the fixed environment of the built-in `bash`, its own process group,
and one ring of the last 256 KiB of its output (standard output and error
together). Output that the ring dropped is reported as lost bytes. The output
the model reads is data, not an instruction.

A job on this machine owns its process group: when the main process of the job
returns, what it left running in the background is killed too, so
`job start "./server &"` ends at once. Start a server as the main process of
the job (`./server`). The same holds when the job is stopped, at its lifetime
and when the server stops. If the server stops without cleaning up, the next
start of the built-in bash kills the groups that the earlier run recorded.
Once `close_input` closed the input of a job, a later `input` says that the
input is closed.

**Limits.** 4 running jobs per chat, 8 per user, 32 per server, each for at most
2 hours. The server counts the jobs that run on it and the Sandbox add-on
counts its own, so the jobs on this machine and the jobs in the Sandbox have
separate limits. The jobs of sub-agents count for the user. A start holds its
place before it runs, so two starts at once never pass a limit together. The
per-user limit of the jobs on this machine is counted on each server. A
finished job stays readable for 30 minutes (at most 16 per chat; the server
keeps at most 128 finished jobs of its own). A job ends when it exits, when the
model or you stop it, at its lifetime, when the chat is deleted, when the shell
tools are switched off for the chat (within 30 seconds), when the chat's agent
gets a folder that confines it, and when the server stops. A job on this
machine also ends when the administrator switches commands on this machine off
(within 30 seconds); that setting does not govern the Sandbox, so it does not
end Sandbox jobs. A job holds the
scratch folder of its chat (on this machine or in the Sandbox): the server does not evict it, put it to sleep or
refresh it while the job runs, so a command run in the same chat meanwhile does
not see files that changed elsewhere until the jobs end. A user has at most 4
such folders with work in them, so jobs run in at most 4 chats of one user at
once; a start in a fifth chat says so.

**Files.** What a job writes reaches your files at the next checkpoint of the
chat: each `bash` call, and one more when the job ends (a change named `Job
job-4k2x7q: build`). A `bash` call that runs while the job writes a file may
save it half written; the next checkpoint corrects it.

**In the Sandbox.** When the Sandbox serves the chat's `bash`, the job runs in
the chat's sandbox, inside the add-on. The sandbox stays awake while a job runs.
It needs a Sandbox add-on of feature level 6; with an older one, `job start`
says so and runs nothing elsewhere (see [Execution provider and status
payload]({{% relref "/addons/contract-reference/manifest-extensions#execution-provider-and-status-payload" %}})).
A call that must make the sandbox again (a new profile, folder or mode) ends the
chat's jobs first and says so at the top of its output.

The add-on does not tell the server when a job ends; the server asks it. Your
list of jobs, the model's `job list`, a wait on the job and a start in the chat
each ask once per chat, and the server that started a job (or saw it running)
asks every 30 seconds while the chat has a running job. That sweep also ends
the chat's jobs when the shell tools are switched off for it, when its agent
gets a folder that confines it (seen on two sweeps in a row), when the chat is
gone, or when the sub-agent that started them has ended; the end reason says
which. The sweep is per server: after a restart of that server, it follows a
chat again from the first time it shows the chat's jobs, and until then the
2-hour lifetime bounds them. Starts, input, kills (with their reason) and the
ends the server sees are written to the audit of the Sandbox jobs; a start the
add-on refused ran nothing and is not written.

A Sandbox job reaches the network through the same proxy as `bash`. The proxy
cannot tell which process opened a connection: while a `bash` call of the chat
runs, a connection of a job counts as that call's, so a network card that a job
causes then looks as if the command of the call asked. When no call runs, a
connection counts as the oldest running job's, and a host that would need your
yes is blocked with the reason `unattended`, even while the model is still
writing its answer; curl or pip print the block into the job's output. The add-on
keeps at most 64 jobs it can save files and reach the network for; a start
beyond that says so and runs nothing.

**Sub-agents.** A sub-agent gets `job` exactly when it gets `bash`, with the
same limits and the same checks. It never sees a card, so on this machine it
runs a job only in mode `on`. Its jobs end with it; the parent can list, read
and kill them, not write to them. See [Jobs and stall waits of
sub-agents]({{% relref "/features/chats-and-agents/chats-sub-agents#jobs-and-stall-waits-of-sub-agents" %}}).

**See and stop a job.** The jobs of a chat are listed above the message box,
with the loops and waits: the label the model gave, the state in words (running,
no output for a while, exited with its status, stopped, killed, lost), where it
runs and for how long. Finished jobs fold into one line. The list never shows
the command. **Stop** ends a running job as you: `term`, then `kill` after 10
seconds, and the end reason reads "stopped". The same is
`POST /api/chats/{id}/jobs/{job}/kill`, for the owner of the chat only. The jobs
are also in the `jobs` field of `GET /api/chats/{id}` and of
`GET /api/chats/{id}/loops`: `id`, `label`, `state`, `end`, `exit`, `started`,
`ended`, `last_output`, `bytes`, `provider` and whether a sub-agent started it.

**Distributed mode.** A job is a process in the memory of one node (or of the
Sandbox add-on), so jobs are per-instance by design: they are not shared and not
moved. A restart of that process or the loss of that node ends the job, and its
output and exit status are gone. The chat record keeps an index of its jobs (id,
node, provider, state, never the command), so a turn that runs on another node
still reads, writes to and kills the job, by a request to the node that runs it.
A node that answers but does not know the job (it restarted) marks the job lost
at once. A node that nobody answers for marks it lost only after three asks
over at least 30 seconds, so a short break does not lose a running job; until
then the model is told to try again and a wait counts a failed check. A read or
a stop of a lost job still asks the node, and a job it still knows is live
again. A wait on a lost job wakes with a text that says the job is gone with its
node and that the condition is not known. If the node only answers slowly,
nothing changes and the model is told to try again.

### Network of the Sandbox commands

The Sandbox has no network device: its commands reach HTTP and HTTPS only
through a proxy that asks LocalAI about each connection (see [Network
access]({{% relref "/addons/catalogue/work/sandbox-settings#network-access" %}})). Each chat has a
**Network** row in its Tools menu with three choices:

- **Off**: commands reach nothing.
- **Custom**: commands reach the package registries (PyPI, npm, Go modules,
  crates.io, RubyGems, Maven Central and GitHub releases), so `pip install`,
  `npm install`, `go get` and `cargo build` work, and also the domains an
  administrator added to the custom list. The list is empty until an
  administrator adds domains.
- **All public sites**: commands reach every public host.

The highlighted choice is what applies: the lower of the choice of the chat and
the limit. When a chat asks for more than the limit (for example All public
sites with the limit Off), the row shows the limit highlighted and says in a
line what the chat asked for. Where commands run on the server's own machine,
the row is a line saying they use its network; network limits need the Sandbox
add-on.

A choice above the limit an administrator set (`chats_egress_ceiling`) is
greyed out with *Limited by your administrator*. When the limit is `off` (the
default), the row says *Off: the administrator has not allowed network access*
and gives an administrator a link to the **Settings** tab of the add-on that runs
the commands, named by its title;
anyone else is told to ask one. The model is told the same, so it reports that
the server's setting blocks the network instead of waiting for an approval. A new chat starts with the
administrator's default. A fork keeps the choice and the allow rules of the
chat it copies. The choice and the rules live with the chat on the server,
never in your files, so the code that runs in the Sandbox cannot change them.
**Rules and requests** under the row lists the hosts this chat allowed or
denied, with a button to remove each, and the requests that asked. The panel of the row shows the custom list under *Custom domains*: the
administrator's extra registries (`chats_egress_extra_registries`) and
always-allowed hosts (`chats_egress_allowed_hosts`), as written. A leading
`*.` matches every descendant of a domain, and a bare `*` means every public
destination on the allowed ports; both stay inside the limit and never reach
private or metadata addresses. They are reachable whenever the network of the
chat is on (Custom or All public sites), without a card; with the network off
they wait until you turn it on. An administrator sees an **Edit domains** link
that opens the network section of the Sandbox settings; anyone else is told
that an administrator manages the list.

The model knows that `curl`, `pip`, `npm` and `git` work in the Sandbox within this setting. It prefers `web_fetch` and `web_search` to read or search web pages when the chat has them, and uses `curl` for APIs, downloads and installs.

When a command asks for a host the chat does not allow but the limit would,
the chat shows a card: *A command in this chat wants to reach* `host:port`,
with **Allow once** (for the rest of that command), **Allow for this chat**
and **Deny**. The command waits up to 60 seconds for your answer and goes on
by itself after it. The card stays with the chat, so a reload shows it; once
answered it becomes one line under the `bash` call that asked, such as
*Allowed for this chat by you, 14:02*. A host inside your network needs an
administrator to allow it. If the limit was lowered while the card waited, the
card has no buttons: it explains that network access is off (or limited) on this
server and who can change it, and it ends as *Refused: the server's network
limit does not allow it*. A trigger, a connector run or a sub-agent never
shows a card.

When commands run on this machine instead, the row is one line, *Commands run
on this machine and use its network*: these settings do not apply there.

The Network row is drawn by LocalAI for the add-on that runs the chat's
commands, when that add-on has the `network-policy` [host
control]({{% relref "/addons/contract-reference/apps-and-pages#host-controls" %}}) (the Sandbox has it).
With an add-on that runs commands but does not have it, the Tools menu has no
Network row; the limit and the default still apply to its commands.

The routes, for the chat's owner (another user's chat is 404):

| Route | What it does |
|---|---|
| `GET /api/chats/{id}/egress` | `{preset, ceiling, effective, applies, applies_reason, addon, registries, allowed_hosts, rules, cards}`. `applies` is true when the add-on that serves `bash` reports, in its typed execution status, that it enforces the network policy and the status is available and isolated (the Sandbox does, except on the host backend). It is false when commands run on this machine (`applies_reason` `host`), no Sandbox serves `bash`, or the add-on does not report enforcement (`none`). `addon` is the add-on that runs the commands and draws the network controls of the chat (it has the `network-policy` [host control]({{% relref "/addons/contract-reference/apps-and-pages#host-controls" %}})); it is absent when no add-on runs the commands or that add-on has no such control. The chat shows the Network row only when `applies` is true and `addon` is set; otherwise it shows a line that says why the settings do not apply. A rule the ceiling no longer admits has `above_limit`. |
| `PUT /api/chats/{id}/egress {preset}` | Sets the preset: `off`, `registries` or `all`. Above the ceiling it is 400 `egress_above_ceiling`. |
| `GET /api/chats/draft/egress?model=&agent_id=` | The network a new chat would start with: the same shape as `GET /api/chats/{id}/egress`, with the default preset (or the saved agent's) under the ceiling, `addon` for the add-on that would run the commands, and no rules or cards. `applies` is always false: no sandbox is bound to a chat that does not exist, so nothing reports enforcement yet. A new chat's composer offers the presets from it; the choice is sent with `PUT /api/chats/{id}/egress` once the chat exists. |
| `DELETE /api/chats/{id}/egress/rules/{host}/{port}` | Removes an allow or deny rule. |
| `POST /api/chats/{id}/egress/consent {id, decision}` | Answers a card: `once`, `chat` or `deny`. Allowing a host inside your network needs an administrator (403). The same answer sent twice is one answer (204, nothing changes). Another answer to a card already answered is 409 `already_answered`. A host the ceiling does not admit refuses the card and gives 400 `egress_above_ceiling` on this answer and on every later one, so a stale card never ends in a conflict loop. In `GET /api/chats/{id}/egress` such a card has `above_ceiling`: no answer can make it work. |

The chat's event stream carries `egress_consent` (`ids: [card id]`, `text:
host:port`) when a card asks and `egress_card` when the chat's network changes,
so every open tab reads it again. Administrators read the settings in force
and the audit with `GET /api/chats/admin/egress` and `GET
/api/chats/admin/egress/events?limit=&user=&chat=&verdict=&host=`, and see
both on the Settings tab of the add-on that runs the commands. Its `sandbox` field names the
add-on that runs the commands now: of the running add-ons that replace `bash`,
the one chosen in `chats_tool_providers` or the one installed first, and none
when the chosen one is stopped. It is read from the add-on host each time, so
it is right after a restart and the same on every frontend of a cluster.

### Sandbox profile

A Sandbox add-on that declares profiles (the Sandbox does: `base`, `media` and
`office`, see [Sandbox profiles]({{% relref "/addons/catalogue/work/sandbox-settings#sandbox-profiles" %}}))
gives the Tools menu a **Profile** row next to the Network row. It shows the
profile the next command runs and lets you pick another one. A new chat gets the
default of the administrator (`media` when the administrator sets none), or the
profile of its saved agent. Switching
restarts the sandbox at the next command: your files stay, running processes and
installed packages are lost, and the row says so before you switch. While the
image of a profile is pulled for the first time, the row shows the progress. If
the profile cannot be used, commands run in `base` and the row says so. A
running chat keeps its sandbox until it restarts, and the row shows when a newer
image is available. A profile the administrator did not give you is shown but
cannot be picked.

The model has one tool for this in the **Run commands** group, `sandbox_profile`:
the action `list` gives the profiles, their programs and whether you may use
each one, and the action `use` switches. It follows the rules of the row.
A saved agent has a `sandbox_profile` field, for its unattended runs and as the
starting profile of its chats.

| Route | What it does |
|---|---|
| `GET /api/chats/{id}/sandbox-profile` | `{applies, addon, profile, selected, source, default, fallback, fallback_reason, newer_image, profiles}`. `source` is `chat`, `agent` or `default`. Each profile has `id`, `title`, `description`, `tools`, the limits, `allowed` and `state` (`phase` `idle`, `pulling` with `percent`, `ready`, `failed` with `error`, or `absent`). `applies` is false when commands run in no sandbox with profiles. |
| `PUT /api/chats/{id}/sandbox-profile {profile}` | Sets the profile, or goes back to the agent's or the default with an empty one. 400 `sandbox_profile_unknown`, 403 `sandbox_profile_not_allowed`, 409 for a sub-agent. |
| `GET /api/chats/sandbox-profiles` | The profiles you may pick for a saved agent or a new chat, and the default. A new chat's composer offers them before the first message and sends the choice with `PUT /api/chats/{id}/sandbox-profile` once the chat exists. |
| `GET /api/chats/admin/sandbox-profiles` | Administrators: the profiles with their limits and the state of their images. |

### Commands on this machine

The built-in `bash` is for a server that cannot run the Sandbox. Read this
before you allow it:

**First start.** While no model is installed, the Home page of an
administrator lists the first steps. The optional last step, **Run code
safely**, offers to install the Sandbox add-on (it opens the add-on browser at
the Sandbox) and says what happens without it: the model can only use the
built-in host `bash` fallback, which has full network access and no isolation.
Press **Skip** to hide the step (the browser remembers it). The step is not
shown when the Sandbox is already installed, or when add-ons are off.

- **No isolation, full network.** A command runs as the user LocalAI runs as.
  It can read every file that LocalAI can read and reach the internet, your
  local network, the LocalAI API on the loopback and cloud metadata addresses.
  Nothing blocks the network without privileges, so there is no "no network"
  option, and the network settings of the Sandbox (`chats_egress_*`) do not
  apply. Install the Sandbox to isolate commands.
- **Never as root.** When LocalAI runs as root, the built-in `bash` is not
  offered.
- **Never for an agent limited to one folder.** A chat whose agent works in
  one folder (and its sub-agents) never gets the built-in `bash`, whatever
  `chats_host_exec` says: a command here can read every file LocalAI can read,
  so nothing keeps it in that folder. Such a chat runs commands only in the
  Sandbox, and its Tools menu says why. See
  [Folders of an agent]({{%relref "/features/chats-and-agents/chats-agents#folders-of-an-agent" %}}).
- **Your files.** Each chat gets a scratch folder with your files: `home/`
  there is `/workspace/home` of the file tools, and `shared-<name>/` is a
  shared folder. A command starts in the chat's work folder, `$WORK`, which is
  `home/Chats/<chat>/work` (`home/` itself when the chat has none). So your
  home is `$WORK/../../..` and a shared folder is
  `$WORK/../../../../shared-<name>`; quote every path. The skills are there
  too, read-only, at the names the file tools use: `shared-skills/` (shared by
  an administrator), `shared-builtin-skills/` (built in) and
  `shared-addon-skills/` (from add-ons), so user-written skill scripts can run in place. Sandbox document
  procedures require the Sandbox guest instead; host commands do not provide
  its dependencies. A
  shared folder named `skills` is at `shared-skills 2/`. After each call, what the command changed is saved to your
  files as one change of the chat, which you can undo in Files, History; a
  file you or the file tools changed meanwhile is read again before the next
  call.
- **A clean environment.** A command gets `PATH`, `HOME`, `LANG` and `TZ`
  only: no API key, database address or token of the server.
- **Limits.** A call stops after `timeoutSeconds` (120 by default, 1800 at
  most), and the whole process group is killed then, when the turn is stopped
  and when the output passes 64 KiB. One call runs at a time in a chat, and at
  most four chats of one user run commands at once.
- **Audit.** Every call is recorded: user, chat, command (cut at 4 KiB), exit
  code, duration and whether it timed out. A line has a `kind`: `run` for a
  `bash` command, and `job_start`, `job_input` (the text written, cut at 4 KiB),
  `job_kill` and `job_end` (exit and duration) for a background job, with its
  `job` id. The record is kept in the auth database (in a cluster,
  shared by every frontend), in a file under the data path, or in memory. The
  server log has one line per command with the prefix `[host-exec]`.
  Administrators read the audit with `GET /api/chats/admin/host-exec/events`
  or the assistant tool `list_host_exec_events`. Lines older than
  `chats_egress_audit_days` (default 30, the retention of the command network
  audit) are removed daily; in a cluster one frontend does it, under an
  advisory lock. The file store also keeps at most 10000 lines.

The runtime setting `chats_host_exec` (Settings, **Tools**, the `bash` row;
`LOCALAI_CHATS_HOST_EXEC`) decides who gets it:

| Value | Who gets the built-in `bash` |
|---|---|
| `off` | Nobody. |
| `ask` | Administrators, in chats they watch. Each command waits for a card in the chat. |
| `on` | Every user who may use chats, also unattended runs that have an agent, and sub-agents. |

Empty is the default: `off` on a server with accounts, `ask` without. In `ask`
mode the chat shows a card under the running call: "Run this command on this
machine?", the command, and **Run once**, **Always on this instance** (saves
`chats_host_exec: on`) and **Don't run**. A person who is not an administrator
can only refuse it. The card lives as long as the call waits, at most 5
minutes. The server keeps it with the chat, so a reload, or another browser
or frontend, shows it again and it can still be answered; once the call ends
the card is gone and the call's line says how it ended. `GET /api/chats/{id}`
carries the waiting card as `host_consent` (`id`, `command`, and `job`, true
when the command starts a [background job](#background-jobs)) while the turn
that asked runs. The card names that turn: a card of an earlier turn is not
shown, also while a later turn runs, and is cleared when the next turn starts.
An answer to a card with nobody waiting on it (its turn has ended, its
frontend stopped, or a newer card replaced it) is `410 consent_expired`, and
the chat shows the card as not answered: the command did not run. The answer
is sent with:

```bash
curl -X POST .../api/chats/3f1c.../host-exec/consent -H 'Content-Type: application/json' -d '{"id":"<id of the host_exec_consent event>","decision":"once"}'
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

Only an administrator may send `once`; anyone who owns the chat may send
`deny`. An answer without the card `id`, or with any other decision, is
`400` for everyone, also for an administrator. The first answer counts: the same answer again is `204`, the other one
`409 already_answered`. The call's line in the chat then says what happened: run on this
machine, allowed once, refused or not answered (card `host_exec`). A call that
the Sandbox served carries the card `tool_provider` with the add-on's ID.

**Distributed mode.** The scratch folders belong to the frontend that runs the
turn, like the Sandbox's sandboxes: your files are the record, and a chat whose
next turn runs on another frontend gets a new folder there. The waiting card is
stored in the chat record (state mode: shared), so any frontend can show it;
its answer is written to the card in the record first and then sent on the
chat bus to the frontend that runs the turn, which also reads the card again
every two seconds, so an answer given through any frontend reaches it within
two seconds even when the bus message is lost, as for the network cards. A card
left by a frontend that died is not shown and cannot be answered: its turn no
longer holds the chat, and the reaper, or the next turn, clears it. The
network cards of the Sandbox follow the same rule. The setting is a runtime
setting like the others.

### What the web interface is told

`GET /api/chats/{id}/addons` says the same for the web interface in
`commands`: `available`, `off` (the selected provider is off for this chat,
or its agent does not name it, and the built-in `bash` is not allowed),
`unavailable` (installed, but it cannot run commands on this server;
`commands_reason` says why) or `none` (no add-on that runs commands is
installed for you). When commands run, `commands_provider` says who serves
`bash` (`builtin`, or the add-on's ID) and `commands_isolation` says `isolated`
or `host`. `commands_consent` is `ask` when each command waits for the card,
and `commands_fallback` with `commands_fallback_reason` name the add-on that
would serve but does not (`off`, or its reason). The **Tools** menu uses this selected-provider response, not private add-on
status data. It labels commands as isolated only when the current provider
explicitly reports available, isolated execution. Missing, loading and unknown
observations remain unknown. Host execution shows a warning without recommending
a specific add-on. The first-run guide and native tool settings do not recommend
an add-on by ID.

```bash
curl -s localhost:8080/api/chats/$ID/addons
# {"addons":[],"commands":"available","commands_isolation":"host","commands_provider":"builtin","commands_consent":"ask"}
```

### The connections of an add-on

Some add-ons run their tools with a token that you keep in a connection (for
example the [GitHub add-on]({{% relref "/features/chat-apps/accounts#github" %}}) or the
[Jellyfin add-on]({{% relref "/features/chat-apps/accounts#jellyfin" %}})). Any add-on
that declares a credential in its manifest works this way. LocalAI never
chooses the connections for you, and a chat can use more than one for the same
add-on:

- With **one** connection for the add-on, it is enabled and cannot be switched
  off separately.
- With **several**, the add-on's row in the **Tools** menu has a **Connections**
  list with a checkbox for each. You tick the ones this chat may use. Until you
  tick one, the tools of the add-on are not offered to the model, the row is off
  and says "Choose at least one connection to use these tools". Ticking a
  connection switches the add-on on.
- With **none**, the row says "No connection yet" and links to the Connectors
  page.
- A [saved agent]({{% relref "/features/chats-and-agents/chats-agents" %}}#connections-of-an-agent) can name a
  set of connections for the add-on. A chat of that agent uses that set, shows it
  read only, and cannot change it. An agent with no set follows the same rules as
  a chat.
- A chat that a connector binding started uses that binding and no other.

The set is stored with the chat, like the other switches, and survives a reload,
a fork and the runs of the triggers that the chat proposed.

**How the model picks one.** The tools keep their names. With exactly one
connection enabled, nothing changes for the model: the tools run with that
connection. With two or more enabled, LocalAI adds one argument, `connection`, to
every tool of the add-on, reads and writes alike. It is required, and its value
is one of the enabled connections by name. The name is the connection's name in
lower case, with each run of other characters turned into a hyphen ("Work
account" is `work-account`); the description of the argument lists each name
with the name you gave the connection. The add-on never sees the argument.
LocalAI checks the name against the connections enabled for this chat, not
against all your connections, so the model cannot use a connection you did not
enable, whatever a tool result or a file says. A missing or unknown name returns
a plain sentence that lists the allowed names, and nothing is sent. A tool that
changes something never runs without the name, also when changes are made
without asking.

The names of your connections to one add-on must differ (case and punctuation
do not make a new name): saving a connection with a name that another has is a
`400`. Connections saved before this rule keep working, and LocalAI gives a
repeated name a numeric suffix in the list the model sees.

The repository (or other resource) list of the connection that was named is the
only one that applies: a call that names a repository that is not on that
connection's list is refused with the list of that connection. The tool result
and the confirmation say which connection acted ("as Work, repository
acme/widgets"). When a change waits for you to confirm it, the card shows the
connection first, and the change is applied with the connection it was previewed
with, even if you change the set meanwhile. The audit line of each use of a
token carries the binding ID.

In `GET /api/chats/{id}/addons` such an add-on has `credentialed: true`,
`connections` (`[{id, name, handle}]`, oldest first; `handle` is the name the
model uses), `enabled_connections` (the IDs this chat may use), `connection_by`
(`chat`, `agent`, `binding` or `only`) and `needs_connection`.
`PUT /api/chats/{id}/connections {"addon": "<add-on id>", "connections": ["<binding id>", ...]}`
sets the set (an empty list clears the choice; `400` when the agent of the chat
names a set or a binding is not yours for that add-on; the older single
`connection` field is read as a set of one). The draft list takes
`connections=<add-on id>:<binding id>`, once for each binding, and
`POST /api/chats` takes `switches.connections` (`{"<add-on id>": ["<binding id>"]}`).
Stored data and requests that hold a single binding ID for an add-on are read as
a set of one. A call that comes from no chat, such as the add-on's own page, can
use a connection only when you have exactly one; otherwise it is refused with
`select_connection`.
