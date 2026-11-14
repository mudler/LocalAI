+++
disableToc = true
title = "Gmail"
description = "Let agents read mail, and save and send drafts with your approval."
weight = 10
keywords = ["gmail", "mail", "google", "add-on"]
+++

The Gmail add-on lets your saved agents read the mail of your Google account. If you allow it, they can also save a draft and send a draft that exists. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | Search mail, read a message or a thread, list labels and read the text of an attachment. Drafts and sends wait for your OK by default. It never deletes, trashes, labels or forwards mail. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. An administrator also saves a Google OAuth client once. LocalAI ships no client. |
| Who turns it on | A person needs the `addons` feature. It is off by default for new users on a server with accounts. The person switches the add-on on for a chat in the **Tools** menu. A saved agent uses it only when you name it in the agent. Each person connects their own Google account. |
| Guide | [Gmail in Chat apps and connections]({{% relref "/features/chat-apps#gmail" %}}) |

Everything the agent reads goes to the model of the chat. Use the label list of the connection to keep an agent to one label.
