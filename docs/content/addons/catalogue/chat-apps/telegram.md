+++
disableToc = true
title = "Telegram"
description = "Bind a Telegram bot to a saved agent, so people can talk to the agent from Telegram."
weight = 10
keywords = ["telegram", "bot", "chat app", "add-on"]
+++

The Telegram add-on binds a Telegram bot to one of your saved agents. A person who messages the bot talks to that agent, and the answer comes back in Telegram. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | A Telegram bot for one saved agent. Each sender gets a conversation of their own, which is a chat of the agent's owner. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `agents` and `connectors` features to make a binding. Both are off by default for new users on a server with accounts. The bot answers only the people on its list of allowed senders. The list is empty when you make a binding. |
| Guide | [Telegram in Chat apps and connections]({{% relref "/features/chat-apps#telegram" %}}) |
