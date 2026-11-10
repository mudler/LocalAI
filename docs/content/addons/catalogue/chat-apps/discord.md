+++
disableToc = true
title = "Discord"
description = "Bind a Discord bot to a saved agent, so people can talk to the agent from Discord."
weight = 30
keywords = ["discord", "bot", "chat app", "add-on"]
+++

The Discord add-on binds a Discord bot to one of your saved agents. People talk to the agent in a direct message with the bot, or by mentioning the bot in a channel or thread of a server. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | A Discord bot for one saved agent. The add-on keeps a Gateway link to Discord, so LocalAI needs no public address. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `agents` and `connectors` features to make a binding. Both are off by default for new users on a server with accounts. The bot answers only the people on its list of allowed senders. The list is empty when you make a binding. |
| Guide | [Discord in Chat apps and connections]({{% relref "/features/chat-apps#discord" %}}) |
