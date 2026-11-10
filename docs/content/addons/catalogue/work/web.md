+++
disableToc = true
title = "Web search"
description = "Add search providers with a key and PDF reading to the web tools of a chat."
weight = 60
keywords = ["web", "search", "brave", "tavily", "pdf", "add-on"]
+++

The Web add-on (ID `web`) serves the chat tools `web_search` and `web_fetch` in place of the built-in ones. The built-in tools work with no add-on and no key. The add-on adds more. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| | |
|---|---|
| What it adds | Search providers that need a key (Brave Search, Tavily and Google Programmable Search), tried in an order an administrator chooses, and the text of PDF files in `web_fetch`. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. An administrator sets the providers on the **Settings** tab of the add-on. |
| Who turns it on | A person needs the `addons` feature. It is off by default for new users on a server with accounts. The person switches the add-on on for a chat in the **Tools** menu. A saved agent uses it only when you name it in the agent. The add-on has its own switch. The **Tools** menu switch of the **Web** row changes only the built-in tools. |
| Guide | [Search the web in chats]({{% relref "/features/chats-and-agents/web#the-web-add-on" %}}) |
