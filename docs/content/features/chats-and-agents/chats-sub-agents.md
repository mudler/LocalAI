+++
url = "/features/chats-sub-agents/"
disableToc = false
title = "Sub-agents in chats"
description = "Let the model hand a self-contained task to a sub-agent and read what it did."
weight = 14
devbuild = "feature"
+++

Let the model hand a self-contained task to a sub-agent, wait for the answer or carry on, and read what the sub-agent did. A sub-agent has fewer tools than its parent when the parent says so, and the number that run at once is limited, as the sections below say.

## Sub-agents

The model can start a sub-agent for a self-contained task. By default it waits
for the answer. With `wait` set to false it carries on, and the result arrives
later as a message in the conversation and as a `sub_agent_done` event.

- A sub-agent is its own conversation, linked to its parent. It appears in the
  list with `kind` set to `subagent` and a `parent_id`.
- A sub-agent gets the parent's tools, or a subset the parent names. It cannot
  start sub-agents of its own.
- A chat runs at most four sub-agents in the background at once. The count
  comes from the shared store, so it holds when turns of the same chat run on
  different frontends of a cluster.
- Stopping a chat, or a turn that fails, stops its running sub-agents, also
  when they run on another frontend and when the parent has no turn left.
- A `sub_agent_started` event (with the run's id and title) and a
  `sub_agent_done` event mark the run in the parent's event stream.
- A sub-agent of a chat whose agent works in [one
  folder]({{%relref "/features/chats-and-agents/chats-agents#folders-of-an-agent" %}})
  works inside the chat's folders, never in more of your files and never
  with more rights than the chat.
- A sub-agent cannot run commands when its parent cannot (see [Running
  commands]({{% relref "/features/chats-and-agents/chats-commands#running-commands" %}})). A task that asks to run something ("execute
  the script", "run make_deck.py") is then refused at once with "the sub-agent
  has no way to run commands either", and no sub-agent starts.
- A task with the same words as an earlier sub-agent of the chat is refused
  while no file of the chat was created or changed since that run: it would
  answer the same. The check reads the store, so it holds on every frontend of
  a cluster.

```bash
curl .../api/chats/3f1c.../agents                      # runs, their state, the limit
curl .../api/chats/3f1c.../agents/9ab2.../messages     # one run's transcript
curl -X POST .../api/chats/3f1c.../agents/9ab2.../stop # stop one run
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

### Jobs and stall waits of sub-agents

- **Waiting for a quiet sub-agent.** `wait_for` takes the id of a background
  sub-agent in `job`, with `until` `exit` (the default) or `stall` and `idle`
  from 60 to 3600 seconds (default 600). Progress is a new row in the
  sub-agent's transcript, read from the store, so the wait pins no node and
  works from any frontend. One long tool call counts as quiet until it ends.
  The wake-up gives the last row, or the state and the last answer of a
  sub-agent that finished; that wake-up is the report of the end, so the
  parent does not also get the "Sub-agent finished" message. `match` is refused. The loops list and its API
  name the source `agent` with the id of the sub-agent.
- **Jobs of a sub-agent.** A sub-agent has the `job` tool exactly when it has
  `bash`, under the same rules: a host job only when commands on this machine
  run with no card (mode `on`), a Sandbox job always, and the act policy of an
  unattended parent applies. The limits are the usual ones: 4 jobs per chat, 8
  per user (the jobs of sub-agents count), 32 per node. The job belongs to the
  sub-agent's chat. A job on this machine runs in the parent's scratch folder,
  where the sub-agent's `bash` runs; a Sandbox job runs in a sandbox of the
  sub-agent's own chat.
- **Jobs end with the sub-agent** (reason `agent_ended`): when its run reaches
  the parent, when the parent or you stop it, and when the sub-agent's chat or
  its parent is deleted. A sub-agent that ends its turn without a `wait_for`
  on its job ends the job; the tool description says so.
- **Job waits.** A sub-agent may arm `wait_for` on its own jobs (not on a
  script). The wait keeps the sub-agent running, so it counts against the
  limit of background sub-agents and the loop limits, and its wake-up is a turn
  of the sub-agent, not of the parent. The parent can wait for the sub-agent,
  not for its job.
- **The parent's view.** `job list` of the parent also shows the jobs of its
  sub-agents, marked `(sub-agent <id>)`. The parent can `read` and `kill` them,
  not write input.

### Saved agents as sub-agents

A chat can also start one of your [saved
agents]({{%relref "/features/chats-and-agents/chats-agents#saved-agents" %}}) as a sub-agent. This
is off for every chat until you switch **Saved agents** on in the chat's
**Tools** menu. The chat then has two more things:

- `list_agents` lists your saved agents: `id`, `name`, `description` and
  `model`, at most 100, and for an agent that works in a folder the folder's
  name (`folder`) and its mode (`folder_mode`, `read` or `write`). It lists only
  your own agents. In a chat whose agent has a folder it lists only the agents
  that can start there (see [Folders of an agent]({{%relref "/features/chats-and-agents/chats-agents#folders-of-an-agent" %}})).
- `spawn_agent` takes an `agent` argument, the id or the name (ignoring case) of
  one of them. The sub-agent runs with the agent's instructions, its model (or
  the chat's model when the agent names none) and only what the agent grants,
  the same as a chat started with that agent. `tools` cannot be given together
  with `agent`. The run is listed with the other sub-agents, with the agent's
  name in its title.

The sub-agent cannot start sub-agents of its own and never gets `list_agents`.
Each call checks again that the chat has the switch on, that an administrator
left `chats_saved_agents` on, and that you have the `agents` feature, as the
agents API does. When one of them fails, or the agent does not exist or is
another user's, the call fails with a message that says why and no sub-agent
starts. Without the `agents` feature the switch is shown greyed out with the
reason.

```bash
curl -s -X PUT localhost:8080/api/chats/$ID/builtins -H 'Content-Type: application/json' -d '{"off":[],"on":["saved_agents"]}'
```

`on` lists the opt-in groups that are on and replaces the earlier list; omit it
to keep them. An opt-in group in `off` is ignored. The group in `GET
/api/chats/{id}/tools` has `opt_in: true`, and `usable` and `reason` say whether
it can serve the chat now.

### Past chats

The group **Past chats** (`history`) lets the model recall what you said in earlier chats. It is off until you switch it on in the chat's Tools menu, like Saved agents. It has two read-only tools:

- `search_chats` (`query`, `scope`, `limit`, `since`) finds messages in your earlier chats with the same search as the [web UI]({{% relref "/features/chats-and-agents/chats-api#search-your-chats" %}}). `scope` is `this_chat` (this conversation, including parts that compaction took out of the model's context), `this_agent` (chats with the same saved agent) or `all`. It returns at most 10 hits; each has `chat_id`, title, date, message number (`seq`), who wrote it and a snippet of at most 300 characters.
- `read_chat` (`chat_id`, `from_seq`, `limit`) reads the user and assistant messages of one chat that a search found, from a message number. It returns at most 20 messages and about 6 KiB, and says which message number to ask for next when more follow.

The model is told to use them only to recall what you said, to say plainly when it finds nothing, and never to quote other people's messages back. Every result is wrapped as untrusted text, because an old message can contain anything: a line in it never changes a switch, a grant or a file.

What the tools never reach:

- Another user's chats. The search applies your account in the query itself.
- Private chats (a chat with the notes of memory switched off), the runs of sub-agents, and every chat that no person started: triggers, jobs, pages and messaging apps. A messaging chat holds the words of other senders, so no chat of a messaging app is returned.
- Deleted chats: a chat leaves the search index in the same step that deletes it, and text that regenerate or edit removed leaves it too.

Who has the tools:

- A chat of yours, when you switch **Past chats** on.
- A saved agent, only when you grant it **Read your earlier chats** (`history.read`), which is off by default. Saving the agent with the grant turns the group on for its chats. An agent that works in a folder may search only its own chats (`this_agent`), never `all`, and `read_chat` refuses chats of other agents.
- Never a run that starts by itself (trigger, job), a chat that a messaging app started, or a sub-agent, whatever its parent has.

Each call writes one log line with the tool, the user, the chat, the scope, a hash of the query, the number of hits and the chat IDs returned. It never holds a query or a message.
