+++
disableToc = false
title = "Connectors and agents"
description = "Let people talk to a saved agent from Telegram, Slack or Discord."
weight = 24
+++

A connector binds a chat platform to a saved agent. People write to the bot, and the agent answers. Telegram, Slack and Discord are add-ons from the library. In the current development builds they are in the development channel.

## What you need

- A saved agent. See [Build your first agent]({{% relref "/start/first-agent" %}}).
- The add-on of the platform, installed by an administrator.
- The `connectors` and `agents` features for your account. They are off by default for new users on a server with accounts. `LOCALAI_DISABLE_CONNECTORS` turns connectors off for everyone.

## One agent for each binding

A binding names exactly one agent. To serve two agents, make two bindings. Two enabled bindings that use the same bot identity do not both answer: only the older one does. If you change the agent of a binding, the next message from each person starts a new chat with the new agent.

A message gets the tools that the agent grants, and nothing else. The chat acts as you, so your quota and model limits apply.

## Who may write to the bot

The list of allowed senders is empty when you make a binding, so the bot accepts nobody. Add chat IDs, user IDs or usernames. **Anyone who can message the bot** is an option, and the server asks you to confirm it, because anyone who finds the bot can then use the tools of the agent as you.

## Files

- **From the sender to the agent.** Files of a message are attached to the chat, as when you attach them on the chat page. They are in your Files.
- **From the agent to the sender.** `send_files` is on by default. After the answer, the files that the turn wrote go to the sender, at most 5 a turn. Only documents, data, pictures, sound, video and `.zip` files go. Programs, scripts and web pages do not. The Slack app needs the `files:write` scope, and a Discord bot needs the Attach Files permission. An older Slack app must be reinstalled to get the scope. Switch the option off in the binding to stop it.
- An agent that works in chosen folders can send and read files only from those folders. Without folders, it works on all your files.

## Collections

An agent searches a collection only when the agent names it. An agent with no collections in its list does not search any. See [Collections]({{% relref "/features/collections" %}}).

## Do not forget

- Text that a connected agent reads or writes goes to the chat model of that agent. If that model is a cloud model, the text goes to that provider.
- Run a test with an allowed sender before you open a bot to others.

For all options, see [Chats: connectors]({{% relref "/features/chats-connectors" %}}).
