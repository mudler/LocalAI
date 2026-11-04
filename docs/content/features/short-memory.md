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
Use [Knowledge](/features/collections/) to search many files.

## How it works

Each turn, the host puts an index of your notes in the prompt, under a fixed header that
asks the model to read the matching topic before it answers. The notes are wrapped as data
you saved, never as instructions. The index is a list of lines
such as `- home: lives in Rome -> home.md`. The model opens a topic with the `context_read`
tool. Two built-in skills, `memory` and `memory-check-own-files`, tell the
model to consult memory for almost every request and to say what it can and cannot read.
While you have no notes, nothing is added to the prompt.

There are three kinds of notes, each in the Memory folder of your files:

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

`GET /api/chats/memory` reads `{enabled, enabled_set, save_mode, save_mode_set, provider,
available}` and `PUT /api/chats/memory` with `{enabled, save_mode}` changes them. The server
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
| **Ask me first** (default) | The assistant proposes the note and you confirm it. |
| **Save it and tell me** | In your own chats the note is saved at once. The chat shows "Saved to memory" with an **Undo**, and no confirmation card. |
| **Never** | The assistant cannot propose a note. It says that saving is off if you ask it to remember something. |

The choice is yours alone and the server keeps it. These rules hold
whatever you choose:

- Chats that come from a messaging app always ask first. The person who wrote the
  message is not always you, so a message from outside can never save to your memory
  without your confirmation. Scheduled and background runs cannot save notes at all.
  The bot writes the question in the chat ("I can save a note ... Reply yes to save it or
  no to drop it"), and a sender whom you trust with the permission **Confirm changes from
  the chat** answers there with `yes` or `no`, within 24 hours. `undo` takes a saved note
  back for 30 minutes. Another sender, or one without that permission, cannot answer: the
  note waits in Studio for you. See
  [Confirming a change in the chat](/features/chats-connectors/#confirming-a-change-in-the-chat).
- A saved agent cannot widen your choice. If you choose to be asked, it asks, even when
  you let it write to memory.
- A reply saves at most 5 notes at once. A long note, or one that looks like it holds a
  secret such as a key or a password, waits for your confirmation instead.
- An automatic note is one change set like any other. The Undo on the chat, the model's
  `revert_change` and the Files history restore the earlier text. Every automatic note
  is in the audit of changes (`GET /api/chats/changes/audit`).

If the server cannot read your choice, it asks first.

## Saved agents and messaging apps

A saved agent reads notes only when its **Add-ons** list names `memory` in the agent
editor (a choice of memory for each agent is planned). A chat that came from a messaging app reads your notes and the notes of its own
conversation, and never the notes of another conversation. A saved agent can only
propose a change when the owner granted it the right.

## Your control

The notes are plain files. Read, edit and delete them on the **Memory** tab of your account
or on the Files page.

## If you used the Memory add-on

The add-on "Short memory" is no longer needed. Its notes, its save choice and its
agent setting keep working, and the server does not load its index files or skills twice.
You can leave it installed or remove it; no note is lost. If you disabled the add-on to
stop memory and never chose otherwise, memory stays off for you.

## For add-on authors

The host serves memory itself. The add-on `memory` stays valid and harmless. An add-on
can still declare `capabilities.contextFiles` for its own index files;
see [Context files](/features/addons/#context-files). A skill named `memory` or
`memory-check-own-files` is reserved for the built-in ones.
