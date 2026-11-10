+++
disableToc = true
title = "GitHub"
description = "Let agents read repositories, issues and pull requests, and comment if you allow it."
weight = 40
keywords = ["github", "repository", "issues", "add-on"]
+++

The GitHub add-on lets your saved agents read the repositories you choose: code, files, issues, pull requests and their diffs. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| | |
|---|---|
| What it adds | Read tools, and, if you allow it, comments on issues and pull requests, new issues, labels and line comments on a pull request diff. It cannot merge, delete, push code, approve or request changes. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `addons` feature. It is off by default for new users on a server with accounts. The person switches the add-on on for a chat in the **Tools** menu. A saved agent uses it only when you name it in the agent. A person adds a connection on the add-on page. A chat uses the connections you switch on for it in the **Tools** menu. The connection holds a fine-grained personal access token and the list of repositories. |
| Guide | [GitHub in Chat apps and connections]({{% relref "/features/chat-apps#github" %}}) |

The add-on does not listen for `@mentions` on GitHub. An agent works on GitHub only when you ask it in a chat.
