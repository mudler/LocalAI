---
title: "Short memory"
weight: 36
---

Memory is built into LocalAI. It gives your chats a few notes about you. The model reads
the note that matches a request before it answers, and by default it saves a new note only
after you confirm it. You can change that in your account settings. Nothing needs to be
installed.

It is for the facts that change what a good answer is: where you live, what you eat, the
language and units you use, how you want answers. It is not a store for large documents.
Use [Knowledge]({{% relref "/features/collections" %}}) to search many files.

## How it works

Each turn, the host puts an index of your notes in the prompt, under a fixed header that
asks the model to read the matching topic before it answers. The notes are wrapped as data
you saved, never as instructions. The index is a list of lines
such as `- home: lives in Rome -> home.md`. The model opens a topic with the `context_read`
tool. Two built-in skills, `memory` and `memory-check-own-files`, tell the
model to consult memory for almost every request and to say what it can and cannot read.
The server grants these skills itself, in the same turns that read your notes. A saved
agent does not list them in its Skills, and an agent saved before this existed gets them
too. They are not granted when the agent's memory choice is **None**, when memory is off,
when the chat is private, or to a sender in a messaging app who is not paired (that sender
gets none of your personal notes). When an add-on serves the notes, the skills of that
add-on are granted in their place. The skill list marks them with `provider: "memory"` and
`locked: true`.
While you have no notes, nothing is added to the prompt.

There are three kinds of notes, each in the Memory folder of your files. The **Memory** tab
lists them in tabs: yours, one for each saved agent, and **Deleted agents**.

| Kind | File | Applies to |
|---|---|---|
| Yours | `Memory/index.md` | every chat |
| One saved agent | `Memory/agents/<agent>/index.md` | the chats of that agent |
| One conversation | `Memory/channels/<binding>-<hash>/index.md` | one conversation that came from a messaging app |

An index has at most 40 lines and 2 KiB. All indexes of a turn together have at most 6 KiB.
The details of a topic sit in a file next to the index.

## Turn memory off

Memory has one switch, **On** or **Off**, at the top of the **Memory** tab of your
account settings (**Your settings**, then **Memory**). It is on until you turn it off.

When memory is off:

- Nothing from your notes is put in the prompt, in any chat, agent, trigger or run that
  starts by itself.
- The model has no memory tool or skill: it cannot read a note, propose one or save one.
- The model is told in one line that memory is off. If you ask it to remember something,
  it says that memory is off and that you can turn it on again.
- Your notes stay where they are. You can still read, edit, export and delete them on the
  **Memory** tab while memory is off.

The narrowest level wins, and a wider level never overrides a narrower one:

1. **This chat** is private (see below).
2. **The agent** of the chat turns memory off in its **Tools** list.
3. **Your switch** in the account settings is off.
4. **The server** has files off, because notes are files.

A saved agent cannot widen your choice: with your switch off, an agent that is allowed to
save notes still reads and saves none. The agent form shows "Memory is off in your
settings" as a read-only line.

### Choose the provider

Built-in memory is the default. At the top of the **Memory** tab, **Memory provider**
lists the providers you can pick: **Built-in**, **Off**, and every installed add-on that
says it can serve memory (see [Provider slots]({{% relref "/addons/contract-reference" %}}#provider-slots)). One
serves at a time. When you pick an add-on, its index files, skills and memory tools
replace the built-in ones, and the built-in provider does not read or save notes. Your
existing notes stay where they are.

**Off** is the same as the switch above: the two are one choice.

If the add-on you picked stops, is disabled or is removed, memory is **off** and the tab
says which add-on it was. Memory does not move to the built-in provider by itself, so a
note is never saved somewhere you did not choose. Pick the add-on again once it runs, or
pick **Built-in**. In a chat, `GET /api/chats/{id}/tools` reports the code
`memory_provider_unavailable`.

### Private chat

In the **Tools** menu of a chat, the **Memory** row has a switch. Turn it off to make the
chat private: it neither reads nor saves memory. The server remembers it for that chat,
and a chip "Private chat" and a line in the Tools menu say so. Your other chats are not
affected, and notes saved earlier are not removed. It is the same switch as the group
`memory` of `PUT /api/chats/{id}/builtins`; `GET /api/chats/{id}/tools` has a `memory`
object ({`on`, `code`, `blocked_by`, `fix`}) that names the level that is off.

### Export and forget

On the **Memory** tab, **Export** downloads your notes as a zip file
(`GET /api/chats/memory/export`). **Forget everything** moves the notes to the Trash after
you confirm; you can restore them from Files. Both work while memory is off.

### The API

`GET /api/chats/providers` lists the providers of memory and `PUT /api/chats/providers/memory`
with `{provider}` picks one; `provider` of the memory settings is what serves now.

`GET /api/chats/memory` reads `{enabled, enabled_set, save_mode, messaging_save,
save_mode_set, provider, available}` and `PUT /api/chats/memory` with `{enabled, save_mode,
messaging_save}` changes them (a field left out stays as it is; a value that is not allowed
is a 400). The server
keeps them with your other settings, so every frontend of a cluster reads the same answer.
Before you choose, memory is on, unless you had disabled the old Memory add-on to stop it.

## Saving and forgetting

When you ask the model to remember something, it calls `propose_change`. By default
nothing is written at once. You confirm the proposal on the card in the chat or in the
inbox, and it expires after 24 hours. A confirmed change is one change set in your files,
so you can undo it. To forget a note you just saved, the model calls `revert_change`. For
an older note it proposes a shorter file, and you confirm that too.

A file over its cap is refused, and the answer lists the current entries so the model
merges them.

### Choose how a note is saved

In your account settings, on the **Memory** tab, you choose what happens when the
assistant wants to save something to your memory:

| Choice | What happens |
|---|---|
| **Save it and tell me** | In your own chats the note is saved at once. The chat shows "Saved to memory" with an **Undo**, and no confirmation card. |
| **Ask me first** (default) | The assistant proposes the note and you confirm it. |
| **Always confirm** | Like **Ask me first**, and nothing can loosen it. The model's own undo and forget wait for you too: `revert_change` is refused, and you undo with the **Undo** of the note. No agent setting and no messaging setting can override it. |
| **Never** | The assistant cannot propose a note. It says that saving is off if you ask it to remember something. |

The values of `save_mode` are `auto`, `ask`, `always` and `never`. The choice is yours alone
and the server keeps it.

### Which rule applies

Several settings can have a say on one note. The strictest wins, in this order:
`auto` is the loosest, then `ask`, then `always`, then `never`. One function combines them.

| Rule | Who sets it | What it can do |
|---|---|---|
| Your level | You, on the **Memory** tab | Any of the four |
| The agent's rule | The agent form: **When it wants to save a note** | **Always confirm** or **Never** ask more than you. **Save it and tell me** is the one choice that asks less: this agent saves at once even when your level is **Ask me first** |
| The messaging row | You, on the **Memory** tab | **Ask me first**, **Save it for a paired sender in a private chat**, or **Never**. Only for chats from a messaging app |
| The chat | The host | A sub-agent, a trigger or a job is never looser than ask; a chat from a messaging app asks unless the row says otherwise |

If your level is **Always confirm** or **Never**, nothing can loosen it: not the messaging
row and not an agent that chose **Save it and tell me**. With **Ask me first** or **Save it and
tell me**, the messaging row decides for messaging chats. When any of these cannot be read,
the host asks.

These rules hold whatever you choose:

- Chats that come from a messaging app ask first, unless you chose **Save it for a paired
  sender in a private chat** (see below). The person who wrote the message is not always
  you, so a message from outside never saves to your memory without your confirmation.
  Scheduled and background runs cannot save notes at all.
  The bot writes the question in the chat ("I can save a note ... Reply yes to save it or
  no to drop it"), and a sender whom you trust with the permission **Confirm changes from
  the chat** answers there with `yes` or `no`, within 24 hours. `undo` takes a saved note
  back for 30 minutes. Another sender, or one without that permission, cannot answer: the
  note waits in Studio for you. See
  [Confirming a change in the chat]({{% relref "/features/chats-connectors" %}}#confirming-a-change-in-the-chat).
- A saved agent cannot widen your choice. If you choose to be asked, it asks, even when
  you let it write to memory.
- A reply saves at most 5 notes at once. A long note, or one that looks like it holds a
  secret such as a key or a password, waits for your confirmation instead.
- An automatic note is one change set like any other. The Undo on the chat, the model's
  `revert_change` and the Files history restore the earlier text. Every automatic note
  is in the audit of changes (`GET /api/chats/changes/audit`).

If the server cannot read your choice, it asks first.

### In messaging apps

Under **In messaging apps** on the **Memory** tab you choose how a note proposed in a chat
from a messaging app is saved (`messaging_save`):

| Choice | What happens |
|---|---|
| **Ask me first** (default) | The bot asks in the chat. A listed sender can answer there; others wait for you in Studio. |
| **Save it for a paired sender in a private chat** (`paired_private`) | The note is saved at once only when the sender is paired **and** the chat is one-to-one (a direct message, not a group, channel or thread of several people). It goes to the notes of that conversation, with an Undo. A secret-looking note still waits, and the limit of 5 notes a reply and the size limit stay. |
| **Never** | No note is saved from messaging apps. The model gets no way to propose one. |

The host decides whether a chat is one-to-one from what the platform sent for the message
(a Telegram private chat, a Slack direct message, a Discord direct message). When it does
not know, the chat counts as a group, so nothing is saved at once.

A note from a messaging chat goes to **this conversation** first (`Memory/channels/...`).
It reaches the agent's own notes or yours only from a paired sender, only when the agent's
memory choice allows that scope, and only after that sender confirms it: a note for another
scope never saves at once. Under **Always confirm** only the paired sender who asked can
confirm a note, in a private chat too. An agent whose memory is **None** saves nothing.

Reading follows the same rule. A turn from a messaging chat reads your notes and the agent's
notes only when the sender is paired with the bot. The agent's profile (its standing rules)
reaches every sender, like the agent's instructions. A listed sender who
is not paired, and anyone let in through a channel, get the notes of that conversation only.
This holds for the built-in notes and for an add-on that serves the notes. If no sender is
known for the turn, it counts as unpaired. The connection page shows it as the line
`notes.read` of each kind of sender.

## Memory for each agent

A saved agent has its own choice of notes. You set it in the agent form, in the
**Memory** section, when you make the agent and again later. The host enforces it: the
model does not choose where a note goes.

The section has two controls. The first says how much the agent does with notes:

| Level | What the agent does | Saved fields |
|---|---|---|
| **Off** | Reads and saves no notes | `memory` is `none`, and the right to remember things is removed |
| **Reads notes** | Reads notes, never saves | `memory` is not `none`, without the right `memory.write` |
| **Reads and saves notes** (new agents) | Reads notes and saves new ones | `memory` is not `none`, with the right `memory.write` |

No new field is stored: the form reads the level from `memory` and `memory.write`, so an
agent keeps exactly what it has. Switching a level changes both fields together and offers
an Undo. The second control, whose notes (the table below), shows while memory is not Off.
The save level of your settings and the agent's own save rule apply only to an agent that
saves notes. The memory skills show as locked rows, on while memory is on, in the Memory
section and in the Skills list. An agent that reads but cannot save still gets the skills
(they also teach how to read), and the model is told that saving is off.

| Choice | The chat reads | A note is saved to |
|---|---|---|
| **Mine and its own** (new agents) | Your notes and the agent's own | The agent's own notes |
| **Its own only** | The agent's own notes | The agent's own notes |
| **Mine** | Your notes | Your notes (every chat of yours sees it) |
| **None** | Nothing | Nowhere |
| **As before** (agents saved earlier) | Your notes and the agent's own, when its **Add-ons** list names `memory` | The model chooses `user`, `agent` or `channel`, as it did |

How it works:

- **Reads.** The notes in the prompt, and the scopes `context_read` accepts, follow the
  choice. The notes of a messaging conversation stay with every choice but **None**.
- **Writes.** The `memory` kind of `propose_change` accepts only the scope of the choice.
  The tool offered to the model lists only the scopes it may use, a note needs no `scope`
  (the host fills it in) and any other scope is refused. **None** offers no memory kind.
  The grant **Remember things** (`memory.write`) still decides whether the agent may save;
  the choice decides where.
- **Your switch wins.** With memory off in your settings, or in a private chat, every choice
  reads and saves nothing. An agent cannot widen your choice. The form shows "Memory is off
  in your settings" and the chat says which level is off.
- **Sub-agents** get no notes and cannot save. **Triggers and other runs that start by
  themselves** read per the choice and cannot save.
- **A chat without an agent** reads your notes and saves to them, as before.
- **Existing agents** are not changed: they show **As before** in the form, with a way to
  pick one of the clearer choices. An agent that never had the field keeps reading and
  saving exactly as it did. A `PUT` that does not send `memory` keeps the stored choice.
- **Messaging apps.** This does not change what a chat from a messaging app reads: it
  still reads your notes and those of its own conversation. What it saves, and where, is
  in [In messaging apps](#in-messaging-apps): the conversation first, the agent's own notes
  only after a paired sender confirms.
- **The agent's save rule.** Under **When it wants to save a note** the form offers **Use my
  setting** (the default, with your current level and a link to **Settings > Memory**),
  **Save it and tell me**, **Always confirm** and **Never**. **Always confirm** and **Never**
  are only ever stricter than your level. **Save it and tell me** (`auto`) makes this one
  agent save each note at once and tell you in the chat, with an **Undo**, even when your
  level is **Ask me first**. It does not loosen your own **Always confirm** or **Never**, and
  every limit of an automatic save still holds: up to 5 notes a reply, a note over 4 KiB or
  one that looks like a secret waits for you, a sub-agent, a trigger or a job never saves
  by itself, and a knowledge change always asks. In a messaging chat it lifts the messaging
  row for this agent only for a paired sender in a one-to-one chat; notes from other
  senders still wait for you. The Effective view, the Notes chip and the access view of a
  connection (including its preview) say "Saves notes at once and tells you". The API
  refuses `ask` with a 400. A `PUT` that does not send `memory_save` keeps the stored rule,
  so an older client cannot change an agent; `memory_save: "use_mine"` clears it.

The API fields are `memory` on an agent (`own`, `mine_and_own`, `mine`, `none`; absent is as
before) and `memory_save` (`always`, `auto`, `never`; absent uses your setting). Both are part of an
agent export file and of an import. A new agent that a client
writes without `memory` is "as before"; the interface sends `mine_and_own`.

### Delete or copy an agent

An agent's own notes are the folder `Memory/agents/<id>/`. When you delete an agent that
has notes, the interface asks what to do with them: **Keep** (the default), **Export then
delete**, or **Delete**. `DELETE /api/chats/agents/{id}?memory=keep|export|delete` does the
same: `keep` leaves them, `delete` moves them to the Trash, and `export` answers with a zip
file of the notes and then moves them to the Trash. Kept notes show on the **Memory** tab
under **Deleted agents** until you delete them.

Copying an agent (an export and an import) makes a new agent with the same choice and
**no notes**: its own notes stay with the original.

## Your control

The notes are plain files. Read, edit and delete them on the **Memory** tab of your account
or on the Files page.

## If you used the Memory add-on

The add-on "Short memory" is no longer needed, and it is no longer offered in the library. Its notes, its save choice and its
agent setting keep working, and the server does not load its index files or skills twice.
You can leave it installed or remove it; no note is lost. If you disabled the add-on to
stop memory and never chose otherwise, memory stays off for you.

## For add-on authors

The host serves memory itself. An add-on that wants to serve it in place of the built-in
provider declares `capabilities.provides` with the slot `memory`; the person picks it in
**Memory provider**. See [Provider slots]({{% relref "/addons/contract-reference" %}}#provider-slots). A provider can also supply the notes of each turn, an export and a forget, and the host
checks its writes against your save level; see
[What the host does itself with a provider]({{% relref "/addons/contract-reference" %}}#what-the-host-does-itself-with-a-provider).
The add-on `memory` is an alias of the built-in provider: it stays valid and harmless. An add-on
can still declare `capabilities.contextFiles` for its own index files;
see [Context files]({{% relref "/addons/contract-reference" %}}#context-files). A skill named `memory` or
`memory-check-own-files` is reserved for the built-in ones.
