+++
disableToc = true
title = "Mail"
description = "Let agents read a mailbox over IMAP and write mail over SMTP, with any provider."
weight = 20
keywords = ["mail", "imap", "smtp", "add-on"]
+++

The Mail add-on lets your saved agents read a mailbox over IMAP and, if you allow it, write mail over SMTP. It works with any provider that offers both protocols. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| | |
|---|---|
| What it adds | List folders, search, read a message or a conversation, read the text of a text attachment, and send or save a plain text email. It never deletes, moves, flags or forwards mail. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `addons` feature. It is off by default for new users on a server with accounts. The person switches the add-on on for a chat in the **Tools** menu. A saved agent uses it only when you name it in the agent. A person adds a connection on the add-on page. A chat uses the connections you switch on for it in the **Tools** menu. You give the IMAP and SMTP server names of your provider. |
| Guide | [Mail in Chat apps and connections]({{% relref "/features/chat-apps#mail-imap-and-smtp" %}}) |

Mail text goes to the model the chat uses.
