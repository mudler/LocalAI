+++
disableToc = true
title = "Slack"
description = "Bind a Slack app to a saved agent, so people can talk to the agent from Slack."
weight = 20
keywords = ["slack", "bot", "chat app", "add-on"]
+++

The Slack add-on binds a Slack app to one of your saved agents. People talk to the agent in a direct message with the app, or by mentioning the app in a channel. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | A Slack app for one saved agent. It uses Socket Mode, so LocalAI needs no public address. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `agents` and `connectors` features to make a binding. Both are off by default for new users on a server with accounts. The bot answers only the people on its list of allowed senders. The list is empty when you make a binding. |
| Guide | [Slack in Chat apps and connections]({{% relref "/features/chat-apps#slack" %}}) |
