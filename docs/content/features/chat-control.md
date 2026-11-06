+++
disableToc = false
title = "Work with a chat: steer, approve, delegate"
weight = 21
url = "/features/chat-control/"
+++

A [chat]({{% relref "features/chats" %}}) is kept on the server and keeps
working while you do other things. This page shows the three things you do
most: steer it while it works, approve what lasts, and let it hand work to
sub-agents. Chats need only the `chats` feature, which is on for every user by
default.

## Steer: keep typing

You do not have to wait for an answer. Send another message while the model
works and it waits in a queue. The queued messages reach the model together,
in the order you sent them, at its next request.

- Until the model has them, you can change or remove a queued message.
- **Stop** ends the running turn.
- After a stop or a failed turn, queued messages stay. Nothing starts by
  itself. Use **send now** to start a turn from them.

Try it: ask for a long summary, and while it streams, send "Use bullet points".
The second message arrives with the model's next request.

## Approve: nothing lasting happens without you

Some tools change what lasts. By default the model only proposes the change.
You see a card in the chat or in your inbox and confirm it.

| The model wants to | What you see |
|---|---|
| Remember a note about you | A memory card. In your account settings, **Memory** tab, you can choose **Save it and tell me**, **Ask me first** (the default), **Always confirm** or **Never**. |
| Change an agent's profile, a skill, a page or your Home layout | A change proposal card. |
| Start a schedule | A trigger proposal. Nothing runs until you confirm it. |
| Reach a host from a Sandbox command | A network card: **Allow once**, **Allow for this chat** or **Deny**. |
| Write to GitHub (comment, open an issue) | An approval card, when the connection's write mode is **Asks you first**. |

A proposal waits 24 hours, then it expires. You can have at most 10 waiting.
A confirmed change is one change set in your files, so you can undo it in
**Files**, **History**.

A trigger run or a bot has no one to ask. It is not offered the change tools,
and a Sandbox command that needs a new host is refused without a card. A
GitHub write from such a run stays a proposal for you.

Try it: type "Remember that I prefer metric units." Confirm the card. Open
**Account**, then **Memory**, to read the note.

## Delegate: sub-agents

The model can start a sub-agent for a self-contained task.

- By default it waits for the answer. It can also carry on, and the result
  arrives later as a message in the chat.
- A sub-agent is its own conversation, linked to its parent. It gets the
  parent's tools, or a subset. It cannot start sub-agents of its own.
- A chat runs at most four sub-agents in the background at once.
- Stopping the chat stops its sub-agents.
- A sub-agent never gets more rights than the chat: it cannot run commands
  when its parent cannot, and it stays in the parent's folders.
- A saved agent can run as a sub-agent only if you switch **Saved agents** on
  in the chat's **Tools** menu.

## Long chats

Long conversations are compacted for you, so a chat can go on past the
model's context window.

More: [Chats]({{% relref "features/chats" %}}),
[Saved agents, triggers and inbox]({{% relref "features/chats-agents" %}}) and
[Short memory]({{% relref "features/short-memory" %}}).
