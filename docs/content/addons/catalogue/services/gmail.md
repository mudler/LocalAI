+++
disableToc = true
title = "Gmail"
description = "Let agents read mail, and save and send drafts with your approval."
weight = 10
keywords = ["gmail", "mail", "google", "add-on"]
+++

Agents can search and read your Gmail. If you allow it, they can also save drafts and send an existing draft.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | Search mail, read a message or a thread, list labels and read the text of an attachment. Drafts and sends wait for your OK by default. It never deletes, trashes, labels or forwards mail. |
| Needs | A Google OAuth client, which an administrator saves once because LocalAI ships none. Then each person connects their own Google account. |
| Guide | [Gmail in Chat apps and connections]({{% relref "/features/chat-apps/accounts#gmail" %}}) |

Whatever the agent reads is sent to the chat's model. To keep an agent to one label, set it in the connection.
