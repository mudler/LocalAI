+++
url = "/features/chats-connectors/access/"
disableToc = false
title = "Who may use a bot"
description = "Choose who may talk to a messaging bot and what it may do for each person."
weight = 40
devbuild = "addon"
+++

Choose who may talk to a messaging bot and what it may do for each person. A new bot answers nobody until you add people. This page covers capabilities, roles and pairing, and the connection page that edits them.

## Who may use the bot

The allowlist decides. It is empty when you make a binding, so the bot accepts
nobody. List chat IDs, user IDs or `@usernames`: numbers for Telegram, IDs such as
`U012AB3CD` and `C0123456` for Slack. A listed group or channel accepts everyone who
addresses the bot in it.

A message from anyone else is ignored: it is not answered, and the sender goes
in the sender log with a count. Open the binding in the list: its pane shows **Senders**, who tried,
and **Allow** adds a person to the allowlist.

"Anyone who can message the bot" (`accept_any`) lets everyone use the agent. The
server asks you to confirm it, because anyone who can find the bot can then use
the agent's tools as you: the request that turns it on, a create with
`accept_any` or an update that changes it from off to on, must also carry
`"confirm_accept_any": "accept any message"`, or it is a `400`. The page does
this with a confirmation. A binding that already has it keeps it on later edits,
and turning it off needs no confirmation. Use it for a public bot whose agent
grants nothing you would mind sharing.

### Capabilities, roles and pairing

Four things sit on top of the allowlist. They work the same for every connector
add-on.

- **A listed sender.** A person who is on the allowlist themselves, by their ID or
  their `@name`, is trusted with every capability that is not sensitive, as far as
  `caps` and the switches of the agent and the server allow it. Nobody needs a
  pairing or a role for that. Someone who is let in only because their channel or group
  is listed, or because the binding accepts anyone, is not a listed sender and gets no
  capability this way.

- **Capabilities** (`caps`). The ceiling you enable for a binding, from a closed list
  (`proposals.confirm`, `memory.write`, `files.upload`, `triggers.propose`,
  `triggers.run`, `talkback.channels`, `tools.write`, `loops.wait`, and the sensitive `triggers.change`, `talkback.new_channel`,
  `profile.write`, `skills.write`, `pages.write`, `dashboard.write`, `knowledge.write`, `loops.start`). The default is
  no: a binding with none enabled lets a sender talk to the agent, but the agent can
  not use any of these on the sender's behalf. A binding saved before permissions
  were enforced has none, so the first start after the upgrade gives it the usual set
  once, the same seven that **Allow the usual** turns on (`proposals.confirm`,
  `memory.write`, `files.upload`, `triggers.propose`, `triggers.run`,
  `talkback.channels`, `loops.wait`; never a sensitive one). It only touches a binding with no
  capability whose kind lists permissions, and it logs one line for each binding it
  changes. It runs once for the whole store, also with several frontends, and never
  again, so a binding you clear later stays cleared. A binding you cleared before that
  start cannot be told from one never set and gets the set too. To keep the strict
  state, set `LOCALAI_SKIP_BINDING_CAPS_MIGRATION=true` before the first start.
  A second one-time migration adds `loops.wait` to each binding that already has some
  capability and lacks it, so a bot that existed before `loops.wait` can use waits
  without a change by you. A binding with no capability stays with none. You can switch
  `loops.wait` off afterwards and it stays off. The same variable skips it.
- **Loops and waits.** `loops.wait` lets the model wait for a change (`wait_for`,
  `wait_agents`). A wait is checked by code and costs no model call until it ends, so
  the capability is not sensitive: a listed or paired sender holds it, and it is on by
  default (in the usual set, and added to existing bindings once). A wait can also
  run a shell command for each check (`script`); it needs a chat that can run
  commands, so a connector chat needs an agent, and it needs a paired sender even
  though a listed sender holds `loops.wait`. The model shows the sender the
  command it armed (see [Chats]({{% relref "/features/chats-and-agents/chats" %}})). `loops.start`
  lets the model start a loop (`start_loop`, then `schedule_next` and `loop_done`).
  Each run of a loop is a model call that nobody asked for at that moment, so the
  capability is sensitive: only a paired sender holds it, and a bot that accepts
  anyone never gives it to an unpaired sender. `loops.start` is off by default and never part of a
  preset: the owner turns it on. A loop that a connector chat started ends after a fifth of
  the run limit, as a loop of a trigger does. The connection page shows the two
  as switches of their own, **Let the agent wait for a change** and **Let the agent
  run on a timer**, apart from the table of who can do what. Both belong to the
  bot alone: a role never holds them, even when the kind lists them (the kind
  list marks them `binding_only`). Waits count against the loops and waits a chat
  and a user may have. See [Loops]({{% relref "/features/chats-and-agents/chats-loops#loops-repeat-a-prompt-in-a-chat" %}}).
- **Roles and grants** (`grants`). An add-on can declare roles, such as the admins of
  a workspace, and report the roles of each sender. You map a role to capabilities
  with `grants: [{"role", "caps"}]`. A grant is within `caps`, within what the kind
  lists as `permissions`, and never a sensitive or a binding-only capability
  (`loops.wait`, `loops.start`). The kind list
  (`GET /api/chats/connectors/kinds`) shows each kind's `roles` and `permissions`.
- **Pairing.** Only a sender you pair may use the sensitive capabilities. Ask for a code
  with `POST /api/chats/connectors/<id>/pairing`; send it as a direct message to the
  bot from an account on the allowlist. The code looks like `LA-` and eight characters,
  works once and lasts ten minutes. The host checks it before any model turn, so
  the code never reaches the agent or the sender log. A binding holds at most three
  paired senders, and a pairing ends after 90 days. End one earlier with
  `DELETE /api/chats/connectors/<id>/paired/<sender>`. A sender who sends five wrong
  codes in an hour is ignored for an hour, and every attempt is logged without the
  code.

Pairing needs only a stable sender ID, so an add-on without roles supports it too.

The host checks these values for every message. A sender may use a capability when
the capability is in `caps`, no switch is off, and:

- for a capability that is not sensitive: the sender is on the allowlist by ID or
  `@name`, or is paired, or has a role that you granted it;
- for a sensitive capability: the sender is paired. The allowlist and a role never
  count.

The allowlist can hold people and channels together. The host matches the sender of
the message against the people entries (ID, and `@name` without regard to case). It
matches the chat to decide whether the bot answers, and to find the trust level of
that channel (below). So a listed channel always lets its members talk to the bot, but
each of them gets capabilities only through a role, a pairing, an entry of their own,
or a channel that is set to let everyone act. A binding with `accept_any` lists
nobody. A sender who is let in but is not trusted still gets a role or a pairing as
before.

#### Channels where the bot listens

Every channel or group on the allowlist (a Slack `C...` or `G...`, a Telegram group, a
Discord channel) has a trust level, `allow_trust` in the API, a map from the entry to
`answer` or `act`:

- `act` (the page says **Let everyone here ask for actions**): everyone who writes in
  the channel, in a thread of it too, is treated like a listed person for the
  capabilities that are not sensitive: sending files, proposing triggers, talk-back and
  the like. The ticked permissions of the connection and the grants of the agent still
  apply, so with an empty ceiling nothing is allowed. The add-on write tools still need
  `tools.write` ticked on the connection (the paired rule is unchanged). Sensitive
  capabilities, such as saving to the owner's notes or knowledge, still need a
  pairing, and the personal notes are still read for paired senders only.
- `answer` (**Answer only**): the bot answers there, but members who are not listed get
  no capability. This is the level of an entry stored before the setting existed, so an
  upgrade never widens trust. It is also the level of an entry that an API client adds
  without `allow_trust`.

On the settings page, **Who can talk to the bot** is one choice, **Only people I choose**
(the default) or **Anyone who can message it** (`accept_any`, with its warning and
confirmation). With the first choice the page shows a table of people and a table of
channels, and one box that adds a name, an `@name` or an ID. Each kind of add-on says what
an ID is: in Slack, an ID that starts with `C` or `G` is a channel and one that starts with
`U` or `W` is a person; in Telegram, a negative ID is a group; Discord cannot tell a person
from a channel by the ID, so it keeps one list, **People and channels**, with the level on
every entry. A person shows as **Verified** or **Listed**, with **Verify** (which makes the
pairing code) and **Remove**. A channel has a level, **Answer only** or **Let everyone here ask for
actions**, and **Remove**. Under the box, the people who wrote to the bot recently and are not
listed show as buttons that add them. None of this adds a field: the page saves `accept_any`,
`allow`, `allow_trust` and the pairings.

A channel that you add on the settings page starts as `act`: allowing a channel is
what you expect to let its people use the bot, and the page shows one sentence under the table: people in a channel act only inside that channel, never in a direct message. The level counts only for messages
written in that channel. A person who is not listed and writes to the bot in a direct
message is not trusted by the channel entry. `accept_any` is unchanged: it accepts
everyone and trusts nobody. The level applies to a group entry only; an `@name` or user
entry has none.

This list is not the list of channels the bot may post to. That list is **Where it may
post** (`channels`, capability `talkback.channels`) and says nothing about whose
messages the bot trusts.

When something is still refused in a channel, the bot says why in one plain sentence:
the channel is set to answer only, the person is not on the list, the action needs a
pairing, or the owner has not turned it on. The access log line carries the sender ID,
the code and the reason, for example that a sender was let in only through a channel set
to Answer only, and the settings page lists the refusal under **Recent refusals** with
**Allow <name>** (it lists the person) and **Let <channel> members act** (it sets the level of the
channel to `act`, in the form, until you save).

The access view and the log name the way a sender got a capability: `allowlist`,
`paired`, `channel` (a member of a channel set to `act`), `agent-connection` (the write
tools of an add-on the agent is connected to) or `role:<id>`. If several people share one turn, the turn gets only what
all of them may do.

A tool of an add-on that holds your credential and changes something outside
LocalAI (a comment on an issue, for example) needs `tools.write`. So does a tool
that changes a resource of an add-on whose manifest scopes its resources per agent
(see [Resources of an agent]({{% relref "/features/chats-and-agents/chats-agents" %}}#resources-of-an-agent)). The
host reads this from the add-on's manifest, so it applies to every add-on of that
shape. The
sender needs the capability as above; the write mode of the connection and the
confirmation of the change still apply on top of it.

The tick of `tools.write` is named "Let people who are not paired change things with
add-on tools", because that is who it is for. Connecting the agent to a connection
of an add-on already says that the agent may use it, so the write tools of that
add-on need no tick for you and for the people you paired with the bot. All of
these must hold:

- the sender is paired with the bot, and the bot lets that sender in today;
- the agent lists the add-on and has a connection of that add-on selected in the
  agent (a connection chosen in the chat, or the only one you have, does not
  count);
- that connection is not in write mode `off`;
- you did not take the tick away after setting it (see below).

The write mode (`propose` or `auto`) and the approval setting keep deciding whether
a call runs at once or waits for a confirmation: this changes only whether the tool
is offered. For every other sender the tick is still needed: a person on the
allowlist who is not paired, a person let in through a channel entry, anyone when the
bot accepts anyone (they never get write tools), a role, a trigger, a job and a
sub-agent. Write tools of an add-on without a connection, such as a table of the
agent, need the tick for paired people too.

If you tick the permission and later take the tick away, that is your choice and it
holds: paired people then have no write tools either. Ticking it again undoes this.
A save that never had the tick changes nothing.

The access view says it for each kind of sender. The paired sender shows the
permission as allowed "because this person is paired and the agent is connected to
the add-on" (the `agent-connection` way), and the other senders show the reason
`connection_permission_off`. The table of the connection page has one sub-row for each
add-on tool. It says that paired people can use the tools because the agent is
connected to the named connection (with its write mode) and that the others need the
tick. When the tools are held back, the model hears which tools and why, so the bot
says "the owner has not allowed this bot to change things in other services" and not
"I have no such tool", and the row says whether the cause is your removed tick, a
connection in write mode `off`, or a missing connection. The same note appears when
the agent names a connection of an add-on that no longer exists.

The agent's grants narrow the result: see Builtin grants in
[Agents]({{% relref "/features/chats-and-agents/chats-agents" %}}). For `triggers.propose`, `triggers.run`,
`talkback.channels`, `talkback.new_channel` and `files.upload` the sender must hold
the capability and the agent must hold the grant.

The check decides which tools the model gets. A tool that the sender may not use is
not offered, so the model cannot be talked into it. If the model calls one anyway, the
call is refused with the reason, and the model tells the person. When a tool is held
back for a missing right, the turn also carries one short note for the model with the
reason in plain words (for example "the owner has not enabled reminders and scheduled
tasks for this bot", or "starting conversations in new channels needs a sender that the
owner paired with this bot"). The model is told to relay it and not to say that it has
no such tool. Today this gates the
agent's change proposals (`memory.write` for a memory note, `profile.write` for a
profile), files sent to the bot (`files.upload`), the trigger tools
(`triggers.propose`), the loop tools (`loops.start` for `start_loop`, `schedule_next` and `loop_done`; `loops.wait` for `wait_for` and `wait_agents`), and the confirm phrase (`proposals.confirm`), which answers trigger proposals and change proposals alike. Messages from a
sender who may not send files get a short refusal and no turn.

A binding that was stored before this check has no capabilities. Enable what you want
with `caps`, and pair the accounts that need the sensitive ones.

The settings page has one table, **Who can do what**, where you set what the bot may do. Its rows are the
permissions and its columns are **On for this bot**, **Verified person**, **Listed person**,
**Channel: everyone acts**, **Channel: answer only**, when the bot accepts anyone,
**Anyone**, and one column for each role the kind declares. The switch of a row in **On for this bot**
is the `caps` ceiling. In a role column, a box is a grant (`grants`): it shows only for a
permission that the kind can give to a role, that is not sensitive, and a box is off until the
switch of its row is on. Each other cell is an icon and a word: Yes, Asks first, No, Waiting for a person or Off. The
page works nothing out for those cells: each one is what the host says in the answer of
`POST /api/chats/connectors/{id}/access/preview`, which the page calls with the form as it is,
including `allow_trust`, `channels` (the places the bot may post) and `approval_mode`. The answer
carries `proposal_mode` (what the connection does with a proposed trigger). A cell that says
No or Off, and in fact any cell, opens the message, the fix and the checks that were not made. The
tools of the add-ons are sub-rows of the permission to change things in connected services. A row has a
**Details** disclosure with the description of the permission and, for **Propose triggers**, the
choices for how a proposal is approved. **Reset to the usual set** puts the switches back to what a new
connection starts with, for the agent of the connection. On a phone the table shows one kind of sender
at a time, chosen with a select.

The settings page uses one set of words. In this document and in the API, a *paired*
sender is shown on the page as a **Verified person**; the saved fields and keys (`paired`,
`allow`, `channels`) do not change. A person on the allowlist is a **Listed person**. A
chat that the bot listens in is a **Channel**. The `channels` list is the **Place the bot
may post**. For triggers and add-on writes, **Asks first** means a person confirms each
change, and **Runs at once** means it runs without asking. The switches of the
capabilities are shown under **On for this bot**.

The settings page of every connector add-on edits all of this: the table above (the switch of
each permission, the role boxes and the cells); the paired senders, with a button that shows a new
code once; the places it may post to (talk-back, **Place the bot may post**); and the section **Check it**,
with **Try it**, **Recent refusals**, the add-on writes that wait for a person and the latest calls of
add-on tools. A permission the agent does not hold cannot be switched on. A link with
`?binding=<id>` opens that binding at its table. The table states the rule: people on the allowlist
can use the ticked capabilities, and a sensitive one also needs the person to be paired; it says
when nobody is paired. A new binding starts with the usual set. The
kind list also returns `capabilities`, the closed list with a title, a description
and the `sensitive` and `usual` flags of each, so a page needs no list of its own.

To see the result, call `GET /api/chats/connectors/<id>/access`. It runs the same check
for a sender on the allowlist by ID or name (`listed`), a sender let in through a
listed channel (`anyone`), a paired sender and a sender of each role the kind
declares, and lists the latest decisions this server made (sender IDs, roles,
capability and result, never message text). An add-on settings page can show
it.

### Why a capability is blocked

Every capability in the access view says `allowed`. A refused one also says why, as data
a page can turn into a sentence and a link, in the first place the check failed:

```json
{
  "capability": "triggers.propose",
  "allowed": false,
  "reason": "the owner has not enabled triggers.propose for this bot",
  "code": "connection_permission_off",
  "blocked_by": "connection",
  "message": "This connection does not allow the bot to set reminders and scheduled tasks. Tick the matching permission on the connection.",
  "fix": {"kind": "open_connection", "target": "caps", "field": "triggers.propose"},
  "then_check": ["sender", "approval"]
}
```

Each kind of sender has one more entry, `notes.read`. It is not a permission that you can tick:
the owner's personal notes (the user and agent notes) are read into a
turn only for a paired sender, so a listed sender who is not paired, an `anyone` sender and a
role sender get `sender_needs_pairing`. The notes of the conversation itself and the agent profile are read for all.

An allowed capability has `allowed: true` and `via` (`paired`, `allowlist` or
`role:<id>`) and none of the other fields. `reason` stays the English line for logs.

The check goes through these links in order and reports the first that fails:

1. `server`: an administrator turned the feature off for the whole server.
2. `tools`: the Tools switch of the agent has the tool group off.
3. `agent`: the agent has not been given the permission.
4. `connection`: the permission is not ticked on this connection (or it has no agent).
5. `sender`: the person is not trusted for it (not on the list, needs a pairing, got in
   through a channel, or has a role without the grant).

`then_check` lists the links after the failed one that were not looked at, so more
problems may follow. For `triggers.propose` it ends with `approval`: the approval mode
shapes the result and never blocks.

| `code` | `blocked_by` | Meaning | `fix.kind` |
|---|---|---|---|
| `server_setting_off` | `server` | The feature is off for the server. `fix.target` is the setting key, `disable_triggers` or `disable_connector_talkback`. | `open_server_setting` |
| `tools_switch_off` | `tools` | The Tools switch of the agent is off. `fix.field` is `tools:<group>`. | `open_agent` |
| `agent_permission_off` | `agent` | The agent lacks the permission. `fix.field` is the permission. | `open_agent` |
| `agent_unavailable` | `agent` | The agent cannot be read. | `open_agent` |
| `no_agent` | `connection` | The connection has no agent. `fix.target` is `agent_id`. | `open_connection` |
| `connection_permission_off` | `connection` | The permission is not ticked. `fix.target` is `caps`. | `open_connection` |
| `sender_not_listed` | `sender` | The sender is not let in. | `add_person` |
| `sender_needs_pairing` | `sender` | A sensitive permission needs a pairing. | `pair_sender` |
| `channel_not_trusted` | `sender` | The sender got in through a channel set to answer only, or because the bot accepts anyone. Add the person, or set the channel to `act`. | `add_person` |
| `role_not_granted` | `sender` | The role of the sender has no grant. `fix.target` is `grants`. | `open_connection` |
| `kind_not_running` | `sender` | The app of the connection is not running, so roles are unknown. | none |
| `context_rule` | `connection` | Held, but not in this place or thread. | none |
| `knowledge_none` | `agent` | `knowledge.write` only: no collection is on the list of the agent. `fix.field` is `collections`. | `open_agent` |
| `knowledge_no_embedding` | `server` | `knowledge.write` only: the embedding model for Knowledge is not installed. | none |
| `unknown_capability` | `server` | Not a capability. | none |

The server sends no URL. A page maps `fix.kind` and `fix.target` to its own links. The
set of codes is closed, and the recent decisions carry `code` too.

The same codes feed the words a sender and the model get when a tool is held back. The
note for the model and the tool error use one whole sentence per code ("The owner has
not allowed this bot to set reminders and scheduled tasks."), with no setting names, host
names or error codes. The note tells the model to relay the reason plainly. The host
never posts it to a channel on its own.

Each capability has a verb phrase ("set reminders and scheduled tasks") that fills the
sentence of every code, so `message` always reads as a whole sentence, for example
"People in a channel are not trusted one by one, so the bot cannot save notes to memory
for them. Add the person to the allowed people."

### Text in the language of the page

The access view, the preview and the kinds call (which lists the capabilities) return
English text and, beside it, the same text in the language of the request. The language
is the `lang` query parameter (for example `it` or `pt-BR`), else the first language of
`Accept-Language` that the host translates, else English. The host translates Italian,
German, Spanish, French, Indonesian, Korean, Brazilian Portuguese and Simplified
Chinese.

- Each decision keeps `code` and `message` and gains `localized_message`, found by
  `code`. The view gains `language`.
- Each capability keeps `title` and `description` and gains `language`,
  `localized_title` and `localized_description`.

The `localized_*` fields are absent for English, for a language the host does not
translate, and for a refusal that a rule of the feature wrote itself (it has no code).
A page shows the localized field when it is there and the English one otherwise. The
note for the model and the words a sender reads in the chat do not use these fields:
the model reads English, and the sender reads the reply language of the connection.

### Where it posts

The `channels` list (capability `talkback.channels`) is a table of **Place the bot may post**: a
name, an ID and one choice that maps to `approved`. **When any allowed person asks** is
`approved: true`, and **Only when a verified person asks** is `approved: false`. The table
shows only while the permission **Post to other channels** is on; otherwise the page says so and
links to the switch. At most 10 places are kept. Listening and posting stay two settings,
but the page shows them together. **Add a place** offers the channels of the listening list that
are not a place yet, and **Other: type an ID**. Each channel of the listening list says whether the
bot may post there, with **Let the bot post here too** to make it a place set to **Only when a
verified person asks**. Under each place a line says whether scheduled runs can post there: they
can only when the place is set to **When any allowed person asks**, because nobody is there to
verify at run time. A place the bot does not listen in is allowed, and the page says the bot will
only post there. **Send my reminders and run results to**
(`notify_chat`) is a choice among the people and channels the page already knows (the allowed
people, the people and chats of the sender log, the places above), with a saved ID that the page
does not know shown as it is and **Another ID...** for a new one. It saves the ID unchanged.

### Preview before saving

`POST /api/chats/connectors/<id>/access/preview` answers like the access view for a form
that is not saved. The body has the fields that the check reads, with the names of the
update call: `agent_id`, `accept_any`, `allow`, `caps` and `grants`. A field that is
absent keeps the saved value; an empty list clears it. `sender` limits the answer to one
kind of sender: `listed`, `paired`, `channel_member` (or `anyone`), `channel_acts`, or `role:<id>`;
an unknown value is a 400. Use the id `new` with a `kind` for a connection that is not
saved yet. The reply has the shape of the access view, with `preview: true` and no recent
decisions.

```json
{"caps": ["memory.write", "triggers.propose"], "sender": "listed"}
```

The preview writes nothing and records no decision. It does not take a token and the
reply never has one. It uses the same sign-in, Connectors feature and routing as the
other connector routes, and the connection of another user is a 404.

### On the connection page

The page of a messaging connection (the Telegram and Slack add-ons) uses these calls in two places.

- **Status** sits at the top of the form: Token, Agent and Who may use it, with a state (OK, needs attention, off, after you save, not working), one sentence and at most one button, and a count of the things to fix. Only the rows that need attention show at first; "Show all" opens the rest. A row that works only once the form is saved says so and offers "Keep these changes", which is the Save button. What each kind of sender may do is in the table, not here.
- **Check it** closes the page. **Try it** has three choices: who (a person, someone in a channel, anyone, or a
  role), where (a direct message or a channel) and what they ask for (a permission of the table). It does not
  call the host again: it reads the answer of the preview for the form as it is, picks the kind of sender
  (`paired`, `listed`, `channel_acts`, `anyone` or `role:<id>`) and writes one sentence from the `message`, `fix`
  and `then_check` of the decision, for example "Refused. Ada Example is a listed person, writing in a direct
  message." followed by the sentence of the host and its button. Nothing is sent to the platform and nothing
  is saved. **Recent refusals** lists what was refused since the server started, from the sender log (a
  sender who is not listed, or a message that did not name the bot) and the access log (a permission that was
  refused), one line each with the person, the chat, the action and the reason. Both logs are kept in memory,
  so they are empty after a restart, and the page says so. The rows of add-on writes that wait for a person
  (waiting, confirmed, cancelled, expired, with who asked and who answered) and the latest tool calls stay in
  the same section.
- **Who can do what** is the table described above. The page calls the preview with the values of the form as they are now, 400 ms after the last change, so it shows what a save will do, and it says so while the form has unsaved changes. A new connection is previewed under the id `new` with its kind. A blocked cell shows the sentence of the host for the first blocker and one button.

The button follows `fix.kind`: `open_connection` jumps to that setting on the page, `open_agent` opens the agent in a new tab (so the form stays as it is), `open_server_setting` opens Settings for an administrator and says "Ask an administrator" for anyone else, `pair_sender` jumps to the pairing button, and `add_person` jumps to the allowed people. Check it stays below the table.
