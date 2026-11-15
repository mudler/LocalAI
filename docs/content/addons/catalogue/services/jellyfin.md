+++
disableToc = true
title = "Jellyfin"
description = "Let agents search a Jellyfin library and see what plays, and control players if you allow it."
weight = 50
keywords = ["jellyfin", "media", "add-on"]
+++

The Jellyfin add-on lets your chats and saved agents look into a Jellyfin media server. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | Search and browse libraries, read the details of an item, see what was added lately and what plays now. If you allow it, control a player, mark an item watched, set a favourite and add items to a playlist. It cannot delete anything, scan or edit the library, or manage users. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `addons` feature. It is off by default for new users on a server with accounts. The person switches the add-on on for a chat in the **Tools** menu. A saved agent uses it only when you name it in the agent. A person adds a connection on the add-on page. A chat uses the connections you switch on for it in the **Tools** menu. The connection holds a Jellyfin API key and the server address. |
| Guide | [Jellyfin in Chat apps and connections]({{% relref "/features/chat-apps#jellyfin" %}}) |
