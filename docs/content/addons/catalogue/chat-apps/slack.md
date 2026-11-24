+++
disableToc = true
title = "Slack"
description = "Bind a Slack app to a saved agent, so people can talk to the agent from Slack."
weight = 20
keywords = ["slack", "bot", "chat app", "add-on"]
+++

Connect a Slack app to one of your saved agents. People talk to the agent in a direct message with the app, or by mentioning the app in a channel.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | A Slack app for one saved agent. It uses Socket Mode, so LocalAI needs no public address. |
| Needs | The Saved agents (`agents`) and Chat connectors (`connectors`) permissions, both off by default for new users on a server with accounts. A Slack app with Socket Mode on, and its two tokens. A new bot answers nobody until you add its allowed senders. |
| Guide | [Slack in Chat apps and connections]({{% relref "/features/chat-apps#slack" %}}) |
