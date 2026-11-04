---
title: "Short memory"
weight: 36
---

Short memory is an add-on that gives your chats a few notes about you. The model reads
the note that matches a request before it answers, and by default it saves a new note only
after you confirm it. You can change that on the add-on page. Install `memory` from the add-on library. Its visible name is **Short
memory**.

It is for the facts that change what a good answer is: where you live, what you eat, the
language and units you use, how you want answers. It is not a store for large documents.
Use [Collections](/features/collections/) to search many files.

## How it works

Each turn, the host puts an index of your notes in the prompt, under a fixed header that
asks the model to read the matching topic before it answers. The index is a list of lines
such as `- home: lives in Rome -> home.md`. The model opens a topic with the `context_read`
tool. The add-on ships two skills, `memory` and `memory-check-own-files`, that tell the
model to consult memory for almost every request and to say what it can and cannot read.

There are three kinds of notes, each in the Memory folder of your files:

| Kind | File | Applies to |
|---|---|---|
| Yours | `Memory/index.md` | every chat |
| One saved agent | `Memory/agents/<agent>/index.md` | the chats of that agent |
| One conversation | `Memory/channels/<binding>-<hash>/index.md` | one conversation that came from a messaging app |

An index has at most 40 lines and 2 KiB. All indexes of a turn together have at most 6 KiB.
The details of a topic sit in a file next to the index.

## Saving and forgetting

When you ask the model to remember something, it calls `propose_change`. By default
nothing is written at once. You confirm the proposal on the card in the chat or in the
inbox, and it expires after 24 hours. A confirmed change is one change set in your files,
so you can undo it. To forget a note you just saved, the model calls `revert_change`. For
an older note it proposes a shorter file, and you confirm that too.

A file over its cap is refused, and the answer lists the current entries so the model
merges them.

### Choose how a note is saved

On the page of the add-on, under **Saving a note**, you choose what happens when the
assistant wants to save something to your memory:

| Choice | What happens |
|---|---|
| **Ask me first** (default) | The assistant proposes the note and you confirm it. |
| **Save it and tell me** | In your own chats the note is saved at once. The chat shows "Saved to memory" with an **Undo**, and no confirmation card. |
| **Never** | The assistant cannot propose a note. It says that saving is off if you ask it to remember something. |

The choice is yours alone and the server keeps it, not the add-on. These rules hold
whatever you choose:

- Chats that come from a messaging app always ask first. The person who wrote the
  message is not always you, so a message from outside can never save to your memory
  without your confirmation. Scheduled and background runs cannot save notes at all.
- A saved agent cannot widen your choice. If you choose to be asked, it asks, even when
  you let it write to memory.
- A reply saves at most 5 notes at once. A long note, or one that looks like it holds a
  secret such as a key or a password, waits for your confirmation instead.
- An automatic note is one change set like any other. The Undo on the chat, the model's
  `revert_change` and the Files history restore the earlier text. Every automatic note
  is in the audit of changes (`GET /api/chats/changes/audit`).

If the server cannot read your choice, it asks first.

## Saved agents and messaging apps

A saved agent reads notes only when you allow the Memory add-on for it in the agent
editor. A chat that came from a messaging app reads your notes and the notes of its own
conversation, and never the notes of another conversation. A saved agent can only
propose a change when the owner granted it the right.

## Your control

The notes are plain files. Read, edit and delete them on the **Memory** tab of your account
or on the Files page. Stopping or removing the add-on stops the model from reading them; it
does not delete them.

## For add-on authors

The add-on is a package like the others: a manifest, two skills and an executable that
serves no tool. It declares `capabilities.contextFiles`, so the host loads the index files.
See [Context files](/features/addons/#context-files). It also declares one user setting,
`save-mode` (`ask`, `auto`, `never`), which the page edits through `sdk.userSettings`; see
[User settings](/features/addons/#user-settings). The manifest needs `api` 2.
