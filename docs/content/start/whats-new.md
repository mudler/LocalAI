+++
disableToc = false
title = "What's new in 5.0"
description = "A plain summary of the ten main changes in LocalAI 5.0, with a link and a tag for each: add-on, built in, or admin switch."
icon = "newspaper"
weight = 140
aliases = ["/basics/news/"]
keywords = ["5.0", "release", "news", "what's new", "team", "add-on", "sandbox", "office"]
+++

This page lists the main capabilities of LocalAI 5.0. Each one has a tag.

| Tag | Meaning |
|---|---|
| **Built in** | Part of LocalAI. No install step. |
| **Add-on** | Comes from the add-on library. An administrator installs it. In the current development builds, the add-ons are in the development channel of the library, so they can change. |
| **Admin on** | Built in, but on a server with accounts an administrator must switch it on for each user. With authentication off, you are the administrator and it is on. |

## The ten capabilities

1. **[Chats]({{% relref "/features/chats-and-agents/chats" %}})** (Built in). The server keeps your conversations. You can keep typing while the model works. The model can hand work to sub-agents, and long chats are compacted for you.
2. **[Saved agents, triggers and inbox]({{% relref "/features/chats-and-agents/chats-agents" %}})** (Admin on). An agent is a set of instructions and exact lists of the tools, add-ons, collections and skills it may use. A trigger runs it on a schedule or when a file arrives. The result goes to your inbox. See also [Build your first agent]({{% relref "/start/first-agent" %}}).
3. **[Files and shared folders]({{% relref "/features/files-and-documents/files" %}})** (Built in). Each user has a workspace with folders, a Trash and a History with Undo. A chat reads and writes these files. You can share a folder with other people.
4. **[Knowledge]({{% relref "/features/files-and-documents/collections" %}})** (Admin on). Collections of documents that an agent can search by meaning.
5. **[Sandbox]({{% relref "/features/files-and-documents/sandbox" %}})** (Add-on). Runs commands and code for a chat in an isolated runtime, with your files mounted. The network is off by default. Linux only.
6. **[Office documents]({{% relref "/features/files-and-documents/office" %}})** (Add-on). Make and edit Word, Excel, PowerPoint and PDF files from a chat, with the Simple Office add-on or the Sandbox.
7. **[Home, pages and triggers]({{% relref "/features/files-and-documents/home-widgets" %}})** (Admin on). Put widgets and your own HTML [pages]({{% relref "/features/files-and-documents/pages" %}}) on Home, and keep them current on a schedule.
8. **[Chat apps]({{% relref "/features/chats-and-agents/chats-connectors" %}})** (Add-on). Connect Telegram, Slack and Discord bots to a saved agent. The `connectors` feature is also off by default for users with accounts.
9. **[Team and sharing]({{% relref "/concepts/users-and-sharing" %}})** (Admin on). Turn on [authentication]({{% relref "/administration/authentication" %}}) to get accounts, roles and a switch for each feature. People share what they own, and nothing is shared by default.
10. **[Add-ons]({{% relref "/addons/user-guide" %}})** (Built in). A separate program that gives LocalAI new tools, pages or services. The host installs it, starts it and limits what it may do. You can [build your own]({{% relref "/addons/build-your-own" %}}).

## If you upgrade

The chats replace the old chat page, the old agents and the old skills. The server converts what it finds at the first start and does not change the old data. See [Moving from the old chat and agents]({{% relref "/features/chats-and-agents/chats-migration" %}}).

## More news

- [Blog](https://localai.io/blog/): release write-ups and engineering notes.
- [GitHub Releases](https://github.com/mudler/LocalAI/releases): the full changelog of every version.
