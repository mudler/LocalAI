+++
url = "/features/chats-connectors/actions/"
disableToc = false
title = "Confirmations, triggers and messages from a bot"
description = "Confirm changes in a connector chat, run triggers from it, and let the agent write to a chat on its own."
weight = 50
devbuild = "addon"
+++

Confirm a change from a connector chat, run triggers that a connector chat proposed, and let an agent write to a chat on its own with `send_message`. Each of these needs a permission on the binding.

## Confirming a change in the chat

When the agent proposes a change that lasts (a memory note, a change of its rules, a
skill, a page or the Home dashboard, with `propose_change`) in a chat from a messaging
app, LocalAI writes the question in that chat. The model does not write it, and it does
not send the person to Studio. A messaging chat asks first by default; the owner can let a
paired sender in a private chat save at once with the row **In messaging apps** (see
[Memory]({{% relref "/features/chats-and-agents/short-memory" %}}#in-messaging-apps)). This section is the answer to the
question.

- **The language.** The question and the replies come in the reply language of the
  binding ([Reply language]({{% relref "/features/chat-apps/answers#reply-language-reply_language" %}})); the examples here are in
  English.
- **The question.** For example: `I can save a note in the memory (every chat of the
  owner): Standup time`, then the lines that will be added (at most 8 lines or 500
  characters), then `Reply "yes" to save it or "no" to drop it, here, within 24 hours. If
  more than one waits, reply "confirm CODE" or "cancel CODE".` The code has four
  characters and is the same on every frontend. Mentions that notify everyone are broken.
- **The answer.** A short word as the whole message, with the same words and languages as
  for triggers: `yes`, `ok`, `confirm` to save, `no`, `cancel` to drop. LocalAI reads it
  before any model turn and the model never sees it. The reply says what happened, for
  example `Saved: Standup time. Reply "undo" within 30 minutes to undo it.`
- **Undo.** The word `undo` (or the phrase of the language, see
  [Reply language]({{% relref "/features/chat-apps/answers#reply-language-reply_language" %}})), alone, within 30 minutes of a save
  that was made from the chat, restores the earlier text (`Removed: ...`). Any other
  word does not undo.
- **Add-on writes.** A write of an add-on that waits because its connection asks first is
  written in the chat the same way and answered with the same words, by a paired sender
  who holds `proposals.confirm`. See [Confirming a held write in the
  chat]({{% relref "/addons/contract-reference/owner-forwarding#confirming-a-held-write-in-the-chat" %}}).
- **Several waiting.** A bare word answers only when exactly one proposal (a change, an
  add-on write or a trigger) waits in that chat and thread. Otherwise the bot lists the codes and the
  person writes `confirm CODE` or `cancel CODE`.
- **Who may answer.** A sender who holds `proposals.confirm` on the binding (a sender on
  the allowlist by ID or name, a paired sender, or a role with the grant). A change of
  the rules of the agent, a skill, a page or the dashboard needs a paired sender. In a
  shared chat only the sender whose message made the proposal can answer; in a private
  chat any trusted sender of that chat can. Another sender gets one sentence, `Only the
  owner can confirm this.` or `Only the person who asked can confirm this.`, and the
  proposal keeps waiting. When the sender who asked cannot confirm, the bot writes no
  question and the proposal waits in Studio for the owner; the model is told so.
- **Notes, scope and pairing.** A memory note from a messaging chat goes to the notes of
  that conversation (`scope` channel) unless the sender is paired and the agent's memory
  choice allows the agent's or the owner's notes. A note for those scopes is confirmed by a
  paired sender only. When the owner chose **Always confirm** (or the agent's rule is
  **Always confirm**), only the paired sender who asked can confirm, in a private chat too,
  and the model cannot undo or forget a note by itself.
- **One-to-one chats.** The host knows a chat is one-to-one from the `direct` field of the
  connector message (a Telegram private chat, a Slack direct message, a Discord direct
  message). The turn carries it, with whether the sender is paired, to the worker. A
  message that does not say counts as a group chat.
- **Time.** A proposal lapses after 24 hours. For 15 minutes after that, a bare word is
  answered with `That proposal expired. Ask again if you still want it.`; later it is
  an ordinary message.
- **Studio.** The same proposal shows as a card in Studio; whichever answer comes first
  wins and the other finds it closed. Every answer is in the audit of changes.

## Triggers from a connector chat

When the binding holds the capability `triggers.propose`, the agent can propose a
trigger from a chat in a messaging app. The agent gets `propose_trigger` and
`list_triggers` (the list shows only the triggers of that agent, and the proposals that
still wait). Nothing runs before the owner confirms.

- **Changing and deleting need `triggers.change`.** It is a separate capability, off by
  default and sensitive: the binding must hold it, and the sender of the message must be
  paired. With it the agent also gets `update_trigger` (a change, a pause or a resume) and
  `delete_trigger` (a delete, or the withdrawal of a proposal that waits), for the
  triggers of that agent only. The approval mode of the binding, the same setting as for
  proposals, decides what happens:
  - `ask` (the default) and `auto_readonly`: the request is a change that waits for the
    owner. LocalAI writes it in the chat with a code, and `confirm CODE` or `cancel CODE`
    answers it, by a sender that holds `proposals.confirm`, in the place where it was
    written. It lapses after 24 hours. Studio shows it on a card too. Even a pause or a
    resume waits.
  - `auto` (**Runs at once**): the change, the pause, the resume, the delete and the
    withdrawal apply at once and LocalAI tells the chat what was done. A change of the acts
    or of the delivery of a trigger still waits for the owner.

  Every result carries the stored state (see
  [What the model is told]({{%relref "/features/chats-and-agents/chats-agents#what-the-model-is-told-and-what-is-recorded" %}})),
  and the log says which mode was used.

- **The proposal is shown in the chat.** LocalAI writes the name, the schedule, what the
  run will do, where the results go and a four-character code in the chat and thread
  where the person wrote. The model does not write it.
- **Answer in plain words.** With the default approval mode (`ask`), the person answers in
  the same chat and thread with a short word as the whole message: `yes`, `ok`, `sure` to
  start it, `no`, `cancel`, `stop` to drop it (a few words of other languages work too).
  LocalAI handles it before any model turn. When several proposals wait in one thread, a
  bare word is not enough: the bot lists the codes, and the person writes `yes CODE`,
  `confirm CODE` or `cancel CODE`. Any longer message goes to the agent as usual. It
  needs the capability `proposals.confirm` for that sender (a sender on the allowlist by ID
  or name, a paired sender, or a role with the grant); a message from any other sender, or text the agent read, never answers
  a proposal. The same words confirm a change of the agent (see
  [Confirming a change in the chat](#confirming-a-change-in-the-chat)). A code is used once, and a proposal lapses after 24 hours. Studio always
  works as well.
- **Permissions are in the text.** The proposal says what a run may do with nobody
  present, in plain words: "Permissions: read only", or the `act.*` classes it carries,
  with the sensitive ones marked. A `yes` grants exactly the listed ones, and the
  confirmation repeats them. The model cannot change them after the confirmation.
  A read only proposal says what the person can do next: ask the agent to let the run
  act (when the binding allows it, see [Runs may act](#runs-may-act)), or change it later
  on the Triggers page.
- **Limits.** A binding can make 3 proposals an hour, and an owner can have 10 waiting.
- **One-off timers.** For "in 10 minutes" or "at 18:00", the agent proposes a trigger
  that runs once: `when` is `{"in": "10 minutes"}` or `{"at": "18:00"}` (this one needs a
  `time_zone`; a time that has passed today means the next day). It must be at least one
  minute and at most 365 days ahead. It reports to the chat where it was set, runs once,
  and is then removed, also when the run failed (a failed timer says so at once). A
  schedule trigger runs at most once every 5 minutes, however it is made. The proposal text states
  the cost: every run is one model run.
- **Where a run reports.** The proposal sets `delivery`: `inbox` (the default),
  `origin` (the chat and thread of the proposal) or `channel` (a channel of the binding that
  the owner approved, named in `delivery.channel`), and a mode: `always`, `on_change` or
  `on_problem`. LocalAI writes the last message of the run, cut to 4,000 characters,
  with mentions that notify everyone broken. A final message of exactly `[SILENT]`
  writes nothing. After three failed runs in a row the chat gets one alert, and not
  more than one every 6 hours. If the binding is off or the capability is off when a run
  ends, the Inbox gets the notice. A binding sends 20 of these messages a minute; more
  wait in a queue and go in order.
- **Pause.** While `triggers.run` is off for the binding, its triggers do not run. The
  records stay and are not replayed when the capability comes back.

### Approval modes

The owner chooses, per binding, what happens to a proposal. The choice is on the settings
page of the connector add-on, nested under the permission **Propose triggers** (greyed while
that permission is off, with its limits in a closed **Limits** list, and the current mode in
the line of the row), or in the `approval` field of the binding
(`PUT /api/chats/connectors/{id}`), and is the same for every connector.

| `mode` | Behavior |
|---|---|
| `ask` (default) | The bot posts the proposal and waits for a plain answer. |
| `auto` | The trigger is made at once. The bot tells the person what was set up and that `cancel` (within 30 minutes) or `cancel CODE` (any time) removes it. |
| `auto_readonly` | Like `auto` when the proposal says its runs only read and report (`access: read_only`, no file written); any other proposal asks. The runs of a trigger made this way can use only tools that read: every other tool is refused at the call, whatever the agent holds (see [Action policy of unattended runs]({{% relref "/features/chats-and-agents/chats-agents" %}}#action-policy-of-unattended-runs)). |

The automatic modes are bounded by limits of the owner. Anything over a limit is not
refused but asked instead, and the proposal says why.

| Field | Default | Meaning |
|---|---|---|
| `max_pending` | 5 | Timers and triggers made without asking that may be live at once, per binding. |
| `min_gap_minutes` | 60 | Fewest minutes between runs of a recurring trigger. |
| `max_runs_per_day` | 10 | Runs a day of all of them together; a recurring trigger also gets this as its daily limit. |
| `max_lifetime_days` | 30 | How far ahead a timer may be, and how long a recurring trigger lives before it is removed. |

A proposal whose runs act follows the rules in [Runs may act](#runs-may-act).
A trigger on files, and one that reports to a channel, always asks. The read-only mode relies on what the proposal says and on
the grants of the agent; it does not stop a run from using a tool the agent has.

### Runs may act

By default the runs of a trigger that a chat proposes only read and report. The owner can
let them do more, per binding, with `approval.acts` in the settings of the connection
(`PUT /api/chats/connectors/{id}`, the same `approval` object as above). It is a list of
the `act.*` permissions that a trigger proposed from a chat of this binding may carry:
`act.files` (write files in the agent's folders), `act.addons` (use the actions of the
agent's add-ons, including running commands through an add-on), `act.send` (send
messages beyond where the run reports) and `act.spend` (use paid or external tools and
the network). The default is an empty list. Any other name is refused with `400`. A
present `approval` object replaces the whole object, so send `acts` again when you change
the mode or a limit.

On the settings page of the connector add-on, **Runs may act** is a switch under the approval mode of the permission **Propose triggers** (greyed while that permission is off). It is off by default. When it is on, four checkboxes name the acts in plain words: write files in the agent's folders, use the actions of the agent's add-ons, send messages beyond where it reports, and use paid or external tools and the network. All four are marked sensitive. A box is disabled with "The agent does not have this." when the agent lacks the matching grant; a box that is already ticked stays ticked and can be unticked. The line of the permission shows the choice, for example "Proposals: set up at once, runs may: write files, use add-ons". The page always sends the mode, the limits and `acts` together.

After a save, the page says what changed, for example "Saved. Changed: permissions, trigger approval and acts." When the save removes an act that the connection allowed, it also lists each trigger that this connection made without asking and that holds that act, with the acts it loses. Those triggers keep running, without the act. A saved connection with an agent also has a link to that agent at the top of its form.

The list is a ceiling, not a grant. The acts of a proposal are what the model asks for in
`acts`, limited to this list, limited to what the agent holds:

- An act the model asked for that the binding does not allow, or that the agent does not
  hold, is dropped. The proposal text names it ("I did not allow: send messages (this
  connection does not allow it)"), so the person is not led to think the run can do it.
  The proposal is still made, with the acts that are left.
- The model sees, in the description of `propose_trigger` and in its result, which acts
  it may ask for here. It is told to ask the person "only report, or also act?" when the
  job clearly changes things.
- The agent is checked again at each run. If it loses a grant after the person said yes,
  the run holds the rest (see [Action policy of unattended runs]({{% relref "/features/chats-and-agents/chats-agents" %}}#action-policy-of-unattended-runs)).
  A trigger made without asking also keeps to the acts of the binding at that moment.

What each approval mode does with a proposal that has acts (`act.files` also counts when
the plan names a file to write). Every act is sensitive:

| `mode` | Proposal with acts |
|---|---|
| `ask` | Asks, as always. |
| `auto_readonly` | Asks. This mode never starts a run that acts. |
| `auto` | Starts at once only when every act is in `approval.acts`, nothing was dropped, and every sender of the turn is paired with the binding (a role or the allowlist is not enough). Otherwise it asks and the proposal says why. |

The limits (`max_pending`, `min_gap_minutes`, `max_runs_per_day`, `max_lifetime_days`)
apply to these triggers as to any other. A proposal that does not ask for acts is not
changed by any of this.

## Writing to a chat on its own (`send_message`)

When the sender of a turn holds the capability `talkback.channels`, the agent gets the
tool `send_message`. The tool names no platform. The agent can use it to:

- **Write in the conversation.** With no `channel`, the message goes to the chat and
  thread of the conversation.
- **Write in a channel.** With `channel`, the message is a new top-level message in a
  channel that the owner listed for the binding (`channels` in the binding: `id`,
  `label` and `approved`, at most 10; an add-on settings page or the API sets them). A
  channel that is not listed is refused, and the error lists the ones that are. `thread`
  answers in a thread of that channel. The agent can name the channel by its label (case
  does not matter and a leading `#` is fine) or by its ID.
- **Write in a channel the owner has not enabled.** A listed channel with `approved`
  false needs the sensitive capability `talkback.new_channel` for each message, so only a
  paired sender can use it. A role never grants it. The owner approves a channel by
  setting `approved` to true.

The agent cannot add a channel or approve one: only the owner changes the list, so
text that the agent read in a web page or a file cannot widen where it writes.

A message has at most 4,000 characters, and mentions that notify everyone are broken.
The message goes through the same queue as other messages that LocalAI writes on its own:
a binding sends 20 a minute, and more wait in order. Each attempt adds a row to the audit
of the binding (time, binding, senders, channel, thread, kind, capability, result, length
and a SHA-256 hash of the text, never the text). `LOCALAI_DISABLE_CONNECTOR_TALKBACK=true`
turns the tool and the reports to a channel off for every binding; owner notices
continue.

A trigger can report to a channel under **Place the bot may post** with `delivery.target` `channel`. The
agent can name the channel by its label or its ID. The description of `send_message` and
of `propose_trigger` lists the approved channels (the first 10) as `label (id)`, so the
agent does not guess an ID. The report is a new message at the top of the channel, never
in the thread of the chat where the trigger was proposed; `delivery.target` `origin` writes
in that thread. The proposal text in the chat and the card in Studio say where the results
go, with the label of the channel. The proposal is refused if the channel is not listed
and approved, and the error lists the approved ones. A channel set to **Only when a verified person
asks** is named in the description as listed but not approved for scheduled runs, and the refusal says
why, so the agent can tell the person that the owner must change that choice. When no channel is
named, the one approved channel is used; with several, the error lists them. The name matches without
case, or by a part of the label that only one approved channel has. `update_trigger` takes `delivery`
and `acts` too, for a trigger that a messaging app proposed, with the same checks; the change waits
for the person on its card. When a run ends, LocalAI
checks again that the binding is on, the capability is on and the channel is still listed
and approved; if not, the Inbox gets the notice.

Not included yet: sending a file with `send_message`, and mapping a reply in the thread of
a message that the agent started to a conversation with the same agent.
