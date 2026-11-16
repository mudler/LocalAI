+++
url = "/features/chats-connectors/accounts/"
disableToc = false
title = "Accounts"
description = "Connect GitHub, Google Drive, Gmail, Jellyfin and Mail so your agents can use them as tools."
weight = 10
devbuild = "addon"
+++

Connect the accounts that your agents use as tools: GitHub, Google Drive, Gmail, Jellyfin and Mail. Each one is an add-on, and you connect it on the **Accounts** tab of **Connections**. Some log in with OAuth. Each section lists the tools and the limits of its connection.

## GitHub

The GitHub add-on lets your [saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}}) read repositories: search code, read files and folders, and read issues, pull requests and their diffs. If you allow it, they can also write: comment on issues and pull requests, open issues, change a title or close an issue, add labels, and comment on a line of a pull request diff. It cannot merge, delete, push code, approve or request changes on a review.

1. Install the **GitHub** add-on from the add-on gallery.
2. On GitHub, make a **fine-grained personal access token** for only the repositories you want to share. The add-on page has a **Create a token on GitHub** button, and the steps and a table of the permissions are open there until you have a first connection. Give the token read access to Contents, Issues and Pull requests. To let the agent write, also give read and write access to Issues and Pull requests. Classic tokens (`ghp_`) are refused unless you allow them on the connection; see [Classic tokens](#classic-tokens).
3. On the add-on page, choose **New GitHub connection**. Paste the token and list the repositories, one `owner/name` per line or separated by commas (at most 50). `owner/*` and `*` are also accepted; see [Any repository](#any-repository). The name of the connection defaults to the first repository; a line that is not `owner/name` is named under the list. Press **Test**: the row shows, for each listed repository, whether the token reaches it and whether it can write.

This connection does not listen for `@mentions` on GitHub yet. An agent works on GitHub only when you ask it in a chat.

A tool-only connection has no agent. The GitHub kind is tool-only, like the Home Assistant, Jellyfin, Mail and PiKVM kinds: the form has no Agent field, and a connection answers no message. Many agents can use one connection. Each agent chooses it in its own **Account it uses** list (see [Agents]({{% relref "/features/chats-and-agents/chats-agents" %}})), and the connection says **Used by N agents** in the list and in its pane, or **Not used by any agent**. The count is of your own saved agents. The server clears `agent_id` on every save and on every read, so an older connection that still held an agent shows none. It cannot be switched on, accept anyone, take an allowlist, send notifications or hold permissions, and the server refuses `enabled` and `accept_any` with a message. This is the manifest key `toolOnly` of the kind (see [Connector add-ons]({{% relref "/addons/contract-reference" %}})). Telegram and Slack bindings always need an agent. Over the API the connection reports `agent_id: ""`, `credentials_only: true` and `used_by`, the number of your saved agents that hold the connection; the kind reports `tool_only: true` and `agent_optional: true`.

The list is checked by LocalAI before the add-on is called, so an agent is refused any repository that is not covered by it, even if the token could reach it. The token is kept sealed and is never shown again, and no tool result contains it. A call uses one of the connections that your chat enabled: with one connection it is enabled, with several you tick the ones the chat may use in the **Tools** menu of the chat (or the agent names a set), and until then the GitHub tools are not offered. With two or more enabled, the model names the connection in each call, and only an enabled one is accepted. LocalAI never picks one for you, and the list of the connection that was named is the only one that applies. See [The connections of an add-on]({{% relref "/features/chats-and-agents/chats-commands#the-connections-of-an-add-on" %}}).

### Any repository

A list entry can stand for many repositories. LocalAI matches the entry itself, before the add-on runs, as GitHub names compare: without regard to case, and only whole names.

| Entry | Covers |
|---|---|
| `owner/name` | that repository |
| `owner/*` | every repository of that owner or organization, one name deep. `other-owner/x` and `owner-evil/x` are not covered |
| `*` | any repository the token can reach, now and later |

A star works only as the whole entry `*` or as the whole name after an owner (`owner/*`). `acme/wid*` and `*/widgets` are refused, and `*` must be the only entry because it already covers the rest. The token still limits what GitHub shows: a fine-grained token reaches only the repositories you picked for it.

`*` needs a confirmation. In the page, tick the box under the list. Over the API, send `confirm_any_resource` set to `allow any resource` in the request that adds `*` (create, or an update that did not have it). The write mode is separate: `*` with **Read only** can change nothing. With `*`, the agent can also list the repositories the token reaches (the `list-repos` tool); with any other list it asks you which repository to use. The Test button reports how many repositories the token reaches instead of testing each one.

This is the manifest key `wildcardResources` of the connector kind (see [Connector add-ons]({{% relref "/addons/contract-reference" %}})). Only the GitHub kind declares it; for every other add-on, the Jellyfin and Home Assistant lists included, each entry stays an exact name. A tool that lists what a credential reaches declares `enumerates` and runs only for a connection whose list is `*`.

### Classic tokens

A classic token (`ghp_`) has scopes for the whole account, not for the repositories you list. By default LocalAI refuses one when you save it, and refuses to use a saved one. You can allow it per connection when a fine-grained token does not fit, for example for an organization that blocks them. Allowing it is a deliberate choice:

- In the page, tick **Allow a classic token** and then the box that says you understand it is wider than the list.
- Over the API, set the option `classicToken` to `allow` in `options`, and send `confirm_wider_token` set to `accept wider token` in the request that turns it on.

The repository list and the write mode still apply, and LocalAI still enforces them before the add-on runs. But GitHub would let a leaked classic token do more than the list, so prefer a fine-grained token. Switching the option off stops the stored classic token from being used at once; the token is not deleted. Fine-grained tokens work as they did and need none of this. This is the manifest key `tokenGuards` of the connector kind, which holds back any token that starts with a prefix until the connection chooses an option.

What GitHub returns (issue text, comments, file content) is written by other people. Every result is marked as untrusted data for the model. Long files are cut at 96 KiB and long patches at 64 KiB; the result says what was cut and how to ask for the rest. When GitHub limits the rate of calls, the agent is told how long to wait.

### Writing to GitHub

On the connection you choose what the agent may change. The choice is stored with the connection and enforced by LocalAI before the add-on is called, so a model cannot raise it.

- **Read only** is the default. A write tool is refused and the agent is told to ask you to turn writes on.
- **Asks you first** stores each change as a proposal. The approval card says what will be done, in which repository and on which issue or pull request (with a link), as which connection, and the exact text that will be posted, and you confirm it in the approvals of the web interface. A chat or an agent cannot confirm it.
- **Acts without asking** lets a chat of yours make the change at once. A run without you present, such as an agent started by a trigger, still leaves a proposal for you to confirm.

The write tools are `comment-issue`, `comment-pr`, `create-issue`, `edit-issue` (title, close or reopen), `add-labels` and `review-comment-pr`. They work only on the repositories listed on the connection, the same list as for reading. The change is also refused when the target moved after the preview: for example the title changed, the issue is locked, or a pull request has a new commit.

Everything the agent writes is public to everyone who can see the repository. The tool descriptions tell the model so. Text is limited to 16 KiB, and a text that holds the token or something shaped like a secret is refused. Every comment and new issue carries a hidden marker (`<!-- localai-op:... -->`) that lets the add-on find an earlier attempt if GitHub took a change but the answer was lost. In that case the proposal shows an unknown outcome and nothing is sent twice.

Over the API the connection has a `write_mode` field: `off`, `propose` or `auto`. Absent means off for a new connection and unchanged for an update.

This version does not listen for mentions.

## Google Drive

The Google Drive add-on lets your [saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}}) read the files in the Drive folders you choose: search, list a folder, read the details of a file and read its text. Google Docs are read as Markdown, Google Sheets as CSV (the first sheet) and Google Slides as plain text. If you allow it, they can also create a text file, replace the text of a file, rename or move a file, and move a file to the trash. It never shares a file, changes who can see it, makes it public or deletes it for good. By default every change waits for your OK; with the write mode **Allow all** a chat of yours makes the change at once.

It logs in to Google with OAuth 2.0 (see [Connections that log in with OAuth](#connections-that-log-in-with-oauth)). Each LocalAI server brings its own Google client; no client is shipped with LocalAI.

### Create the Google client (administrators)

1. In the Google Cloud console, create a project and turn on the **Google Drive API** for it.
2. Set up the **OAuth consent screen**. Add the scope `https://www.googleapis.com/auth/drive.readonly`, and the scope `https://www.googleapis.com/auth/drive.file` if people may write. Then **publish the app to production**. While the app is in the *Testing* state, Google ends every refresh token after 7 days, and the connections stop working until each person connects again.
3. Create a client of the type **Web application**. As the authorized redirect URI, enter the callback address of your server: the base URL followed by `/api/chats/connectors/oauth/callback`, for example `https://localai.example.test/api/chats/connectors/oauth/callback`. The **Settings** tab of the add-on shows it as the **Callback address**. Set the external base URL of the server first if the form says it does not know its public address (see [the login routes]({{% relref "/addons/contract-reference/manifest-extensions#the-login-routes" %}})).
4. On the **Add-ons** page open **Google Drive**, select the **Settings** tab, enter the **Client ID** and **Client secret** of the client and select **Approve and save**.

`drive.readonly` is a restricted scope. An app that stays unverified can still be used, but Google shows a warning at sign-in and limits the app to 100 users. If you need more users, or the app belongs to an organization outside your own Google Workspace, the app must pass Google's verification for restricted scopes, which includes a security assessment. In a Google Workspace you can set the user type to **Internal**, which needs no verification and allows only the people of that organization.

### Connect an account

1. Install the **Google Drive** add-on from the add-on gallery.
2. On the Google Drive page select **Connect with Google**, name the connection, and select **Connect with Google** again. Your browser goes to Google and returns to the Google Drive page. (The **Accounts** tab has the same **New connection** in the Google Drive group.) Tick **Also allow** for the write access if the agent may change files. Without it the connection reads only, and a write tool answers with a message that names the missing access, so the model can ask you to reconnect.
3. Select **Edit connections**. On the add-on page paste the folders the agent may use, one folder ID or folder address per line (at most 50), and choose the write mode. The ID is the part of the address of a folder after `/folders/`; use `root` for the top of My Drive. The ID of a shared drive also works. IDs are case sensitive. To let the agent use every file your Google login can see, in My Drive and in shared drives, tick **All folders (My Drive and shared drives)** instead of listing folders. This is a wider grant, so the page asks you to confirm it once, and the folder box is hidden while it is on. Press **Test** to see the account the connection belongs to.

The agent is optional. A connection with no agent and nothing that needs one (no listening, no allowlist, no permissions, and the trigger approval left at its default of asking you) saves as it is: it only holds the login, the folder list and the write mode for your chats. LocalAI asks for an agent only when you set something that needs one, such as a trigger approval that runs without asking.

All folders is stored as the single entry `*` of the connection's `resources` list (the kind declares `wildcardResources`, see [Connector add-ons]({{% relref "/addons/contract-reference" %}})); a request that adds it must also carry `confirm_any_resource` set to `"allow any resource"`. An empty list still means no access. An agent names the entry as the `folder` argument of a tool: `*`. It may also name one folder ID, and the tool then stays inside that folder.

The list is checked by LocalAI before the add-on is called, so an agent is refused any folder that is not on it. The add-on then checks that each file it reads or changes lies inside that folder, at any depth, and refuses a file outside it. With All folders there is no single folder to hold a file to: the list of the connection and your Google login are the limit, and a search covers every drive that the login reaches. A shortcut is not followed. A search can come back with fewer items than asked, because files outside the folder are left out of each page.

### Tools

| Tool | Kind | What it does |
|---|---|---|
| `search` | read | Finds files by name, text, media type, change date or parent folder. Trashed files are left out unless asked for. 30 results per page, up to 50, with a cursor for the next page. |
| `list-folder` | read | Lists a folder, folders first, with a cursor. |
| `get-metadata` | read | Gives the name, type, size, change time, parent and link of a file. |
| `read-file` | read | Reads the text of a file, by byte offset. Binary files that cannot be exported give the details only. |
| `create-file` | write | Creates a text file (plain text, Markdown, CSV or JSON, up to 64 KiB). |
| `update-file` | write | Renames a file or replaces its text, or both. Only plain text files can have their text replaced. When a call does both, the text is replaced first; if the rename is then refused, the answer says that the text was updated and the rename was refused, and a new call with the new name only retries the rename. |
| `move-file` | write | Moves a file or folder to another folder inside the allowed folder. |
| `trash-file` | write | Moves a file, not a folder, to the trash, where you can restore it. |

A search filter is a set of fields, never a Drive query. The add-on writes the query itself and escapes every value, so a name that holds a quote or a backslash cannot add a condition.

What Drive returns (names and file text) is written by other people. Every result says so and tells the model to treat it as data. A text longer than 96 KiB is cut: the result says so and gives `nextOffset`, and the agent reads the next part with `offset`. The add-on asks for items of shared drives in every call.

### Writing to Drive

The write tools need the optional access `https://www.googleapis.com/auth/drive.file`, which you allow when you connect. Google gives that access only to the files that the add-on created, so `update-file`, `move-file` and `trash-file` work on files made through this add-on; on any other file the tool answers that the change was refused and nothing was sent. The read access reaches all files in the folders.

You choose the write mode on the connection:

- **Read only** (the default) refuses every write tool.
- **Asks you first** stores each change as a proposal, and you confirm it in the approvals of the web interface. A chat or an agent cannot confirm it.
- **Allow all** lets a chat of yours make `create-file`, `update-file`, `move-file` and `trash-file` changes at once, without a card. LocalAI cannot undo a change that was made, and the page says so. A run without you present, such as an agent started by a trigger, still leaves a proposal. Allow all is never chosen for you: a new connection starts on **Read only**, and a connection saved before this mode existed keeps the mode it had.

The approval card shows the file, the folder and the exact text that will be saved. The change is refused when the file changed after the preview. A created file carries a private property that names the operation, so if Drive took a change and the answer was lost, the add-on finds the file and does not create it twice.

### Limits

A call ends after 30 seconds. A request or a result is at most 1 MiB, and a result that the model reads at most 128 KiB. A page has at most 50 entries. A Google Docs export is limited by Google to 10 MB. Finding out where a file lies takes at most 150 requests to Drive per call. When Drive limits the rate of calls or fails, the agent is told how long to wait (the `Retry-After` of Google, or 30 seconds for a limit and 5 seconds for a failure). When Google refuses the access token, LocalAI renews it once; when that fails the connection asks you to reconnect. The add-on never logs a token or the content of a file.

## Gmail

The Gmail add-on lets your [saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}}) read the mail of your Google account: search it, read a message or a whole thread, list the labels and read the text of an attachment. If you allow it, they can also save a draft and send a draft that exists. By default every draft and every send waits for your OK; with the write mode **Allow all** a chat of yours saves and sends at once. It never deletes, trashes, labels or forwards mail, and it never changes a setting or a filter.

{{% callout style="warning" %}}
**A connection reaches the whole mailbox the account can read, unless you list labels.** Mail is the most private text most people have: password resets, bank letters, health, work. Everything the agent reads goes to the model of the chat. Use the label list below to keep an agent to one label, and write `ALL` only for an agent you trust with the whole mailbox. The label list is a limit on what the agent may ask for. It is not a way to hide mail from a model that you give a label to.
{{% /callout %}}

It logs in to Google with OAuth 2.0 (see [Connections that log in with OAuth](#connections-that-log-in-with-oauth)). Each LocalAI server brings its own Google client; no client is shipped with LocalAI.

### Create the Google client (administrators)

The client is made in the same way as for [Google Drive](#create-the-google-client-administrators), and one client can serve both add-ons. In the same Google Cloud project:

1. Turn on the **Gmail API**.
2. Add the scope `https://www.googleapis.com/auth/gmail.readonly` to the **OAuth consent screen**, and the scope `https://www.googleapis.com/auth/gmail.compose` if people may write. Publish the app to production, for the reason given in the Drive section (a refresh token in the *Testing* state ends after 7 days).
3. Use the same client, or create another of the type **Web application** with the same authorized redirect URI (the callback address that the **Settings** tab shows).
4. On the **Add-ons** page open **Gmail**, select the **Settings** tab, enter the **Client ID** and **Client secret** and select **Approve and save**. Each add-on keeps its own copy of the client.

`gmail.readonly` is a restricted scope, and so is `gmail.compose`. An app that stays unverified can still be used, but Google shows a warning at sign-in and limits the app to 100 users. If you need more users, or the app belongs to an organization outside your own Google Workspace, the app must pass Google's verification for restricted scopes, which includes a security assessment. In a Google Workspace you can set the user type to **Internal**, which needs no verification and allows only the people of that organization.

### Scopes

| Scope | Needed for | Why this one |
|---|---|---|
| `gmail.readonly` | all read tools (required) | It reads mail and labels and changes nothing. |
| `gmail.compose` | `create-draft`, `send-draft` (optional, you tick **Also allow**) | The least scope that creates a draft and sends one. `gmail.send` alone cannot reach drafts. |

The add-on asks for no other scope. In particular it does not use `gmail.modify` or the full `https://mail.google.com/` scope, so it cannot change labels, move mail to the trash or delete it. Without the optional scope a write tool answers with a message that names `gmail.compose`, so the model can ask you to reconnect.

### Connect an account

1. Install the **Gmail** add-on from the add-on gallery.
2. On the Gmail page select **Connect with Google**, name the connection, and select **Connect with Google** again. Your browser goes to Google and returns to the Gmail page. (The **Accounts** tab has the same **New connection** in the Gmail group.) Tick **Also allow** for the write access if the agent may save and send drafts.
3. Select **Edit connections**. On the add-on page list the labels the agent may use, one label per line (at most 50), and choose the write mode. A label is written by its name, for example `Work`, or by its ID, such as `Label_12`; system labels are `INBOX`, `STARRED` and so on. Write a name with spaces with dashes: `Private-notes`. The case does not matter. The word `ALL` gives the whole mailbox. An empty list gives nothing. Press **Test** to see the account the connection belongs to.

The list is checked by LocalAI before the add-on is called, so an agent is refused any label that is not on it. The add-on then checks that each message it returns carries the label, and refuses one that does not; a thread shows only the messages with the label; a search is made inside the label. Spam and trash are never searched. A label that does not exist is refused, so a typo never widens the reach. With `ALL` there is no such check.

### Tools

Every tool takes the argument `label`, the entry of the list that the call works under.

| Tool | Kind | What it does |
|---|---|---|
| `search-messages` | read | Finds mail by sender, recipient, subject, text, a second label, dates, attachment and unread. 30 results per page, up to 50, with a cursor. Each result has sender, subject, date and a snippet. |
| `get-message` | read | Gives the headers (From, To, Cc, Reply-To, Subject, Date, Message-ID), the text and the list of attachments (name, type, size and `attachmentId`). A text above 64 KiB is cut and read on with `offset`. |
| `get-thread` | read | Gives the messages of a thread, oldest first, up to 50. Each text is cut so that the result fits, and the result says what was cut. |
| `list-labels` | read | Lists the labels of the mailbox, or the one label of the connection. |
| `read-attachment` | read | Reads the text of an attachment of a text type (text, JSON, CSV, XML, up to 4 MiB) in parts of 96 KiB by byte offset. Any other type gives the details only. |
| `create-draft` | write | Saves a draft: to, cc, subject and a plain text body up to 64 KiB. With `replyToMessageId` the draft joins that thread, with `In-Reply-To` and `References`. It does not send. |
| `send-draft` | write | Sends a draft that exists, as it is when you approve. |

A search filter is a set of fields, never a Gmail query. The add-on writes the query itself, puts every value in double quotes and removes quotes, backslashes and control characters from it, so a value cannot add an operator such as `in:anywhere` or leave its quotes. The label filter is not part of the query: it goes in the `labelIds` parameter.

### What the agent reads

Mail is written by other people, and some of it is written to steer a model. Every result says that it is text written by others and must be treated as data. In addition the add-on:

- returns text only. HTML is converted to text; scripts, styles, images and tracking pixels are dropped, link targets are not shown, and text that the message hides (`display:none`, zero size, transparent) is dropped. The add-on never loads a remote image or follows a link;
- never puts the bytes of an attachment in a result, except the text of a text attachment that the model asks for with `read-attachment`;
- removes invisible and direction-changing characters from text, headers and file names;
- never logs a token, an address or the content of a mail.

### Drafts and sending

The write tools need the optional access `gmail.compose`. The write mode of the connection decides who approves:

- **Read only** (the default) refuses `create-draft` and `send-draft`.
- **Asks you first** stores each draft and each send as a proposal that you confirm in the approvals of the web interface.
- **Allow all** lets a chat of yours save drafts and send them at once, without a card. A sent mail cannot be taken back, and the page says so. A run without you present still leaves a proposal. Allow all is never chosen for you, and a connection saved before this mode existed keeps the mode it had.

With **Asks you first**, sending takes two approvals on purpose: `create-draft` saves a draft that you can also see and edit in Gmail, and `send-draft` sends a draft by its ID. The approval card of a send shows the recipients (To, Cc and Bcc), the subject, the text and the names of the attachments of the draft as they are in Gmail at that moment. The send is refused when the draft changed after the preview.

Addresses must be plain (`name@example.com`, no display name), at most 20 recipients in all, and no address, subject or header may hold a line break, so a model cannot add a hidden recipient. The body is plain text and is sent encoded, so no line of it can be read as a header. A draft with more than 20 recipients, or none, is not sent. The tools cannot add an attachment.

Neither tool can be repeated safely, so the add-on writes a note before it sends the request. If the answer is lost, the host sees an outcome that is not known and a second attempt is refused with a message that says to check Drafts and Sent. The one case it settles alone is a send whose draft has disappeared: sending removes the draft, so the add-on reports that an earlier attempt sent it.

### Limits

A call ends after 30 seconds. A request or a result is at most 1 MiB, and a result that the model reads at most 128 KiB. A page has at most 50 entries; a search reads the headers of each result with one request, six at a time. When Gmail limits the rate of calls or fails, the agent is told how long to wait (the `Retry-After` of Google, or 30 seconds for a limit and 5 seconds for a failure). When Google refuses the access token, LocalAI renews it once; when that fails the connection asks you to reconnect.

## Jellyfin

The Jellyfin add-on lets your [saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}}) and your chats look into a [Jellyfin](https://jellyfin.org) media server: search the library by title, browse a library, read the details of a film, a show or an album, see what was added lately, what a user can resume and which episode is next, see which players are active and what they play, and read the server version. If you allow it, they can also remote control a player (play an item, pause, resume, stop, seek, set the volume), mark an item watched or not watched, set a favourite and add items to a playlist. It cannot delete anything, scan or edit the library, or manage users. It needs Jellyfin 10.9 or later.

1. Install the **Jellyfin** add-on from the add-on gallery.
2. In the Jellyfin dashboard, as an administrator, open **Advanced**, then **API Keys**, choose the plus button, name the key (for example `LocalAI`) and copy it. A Jellyfin API key works for the whole server and not for one user; the write mode below is what limits the agent. Deleting the key in the dashboard cuts the access at once.
3. On the add-on page, choose **New Jellyfin connection**. Paste the key and write the **server address** as you open it in a browser, with the port, for example `http://192.168.1.10:8096` or `https://media.example/jellyfin`. Press **Test**: the row says whether the server answers, which version it runs and whether it accepts the key.

One connection holds one key and one server. To use a second server, add a second connection; when several are enabled in a chat the model names the connection in each call (see [The connections of an add-on]({{% relref "/features/chats-and-agents/chats-commands#the-connections-of-an-add-on" %}})). The agent field is optional, as for GitHub: a connection without an agent holds credentials only.

**The address is the access list.** The server address is the one entry that the connection lists, and LocalAI checks it before the add-on is called, like a repository of a GitHub connection. The add-on talks to that address and to no other: only `http` and `https` are accepted, an address with a user name, a password or a query is refused, the key goes only in the `Authorization` header and never in an address, the paths are built by the add-on, and a redirect is not followed. A plain `http` address sends the key unencrypted; the page says so. Use it only on a network you trust.

**Self-signed certificates.** The add-on checks the certificate of an `https` server as a browser does. If your server uses a self-signed certificate, type its SHA-256 fingerprint in the **Certificate fingerprint** box (from the certificate view of your browser; colons and capitals are fine). The connection then accepts that one certificate and no other, and a different certificate is refused. Checks are never switched off. The fingerprint is stored with the address as `https://host:8920#sha256=<hex>`; this is what a model must copy as `server`, and the tool descriptions tell it to copy the address from the page.

### What the agent sees

Results are short and bounded: at most 50 items a page with a `next` index, overviews cut at 240 characters in a list and 1500 in the details of one item, 20 people per item. They hold names, years, ratings, runtimes, the watched state of the named user and the playback state of a session. They hold no file path, no stream address, no image address and never the key. Titles, overviews, names of people, user names and device names are written by other people, so every result is marked as untrusted data for the model and the add-on's skill says the same. The user of user-specific lists is chosen by name (`list-users` gives the names) and the add-on turns it into an ID; with one user on the server the name can be left out.

### Changing things on Jellyfin

The write mode of the connection works as for GitHub: **Read only** (the default), **Asks you first** and **Acts without asking**, stored with the connection and enforced by LocalAI before the add-on is called. With **Asks you first** each change is a proposal with a card that names the player, its user and what it plays now (for playback), or the item, the user or the playlist, and you confirm it in the approvals. A proposal expires after five minutes. The add-on reads the target again when you confirm: if another item plays on that player now, or the playlist changed, nothing is sent and the card says so. **Acts without asking** lets a chat of yours act at once, which includes pausing or stopping a screen other people are watching; a run without you present still leaves a proposal. The write tools are `control-playback`, `mark-played`, `set-favorite` and `add-to-playlist`.

The connection has a `write_mode` field over the API: `off`, `propose` or `auto`.

## Mail (IMAP and SMTP)

The Mail add-on lets your [saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}}) read a mailbox over IMAP and, if you allow it, write mail over SMTP. It works with any provider that offers both protocols. The provider is not part of the add-on: you give it the server names.

What the agent can do:

- **Read:** list the folders, search a folder (sender, recipient, subject, text anywhere, a date range, unread only, with an attachment), read one message, read the other messages of its conversation in the same folder, and read the text of a text attachment. Searches return 30 messages a page, up to 50, and a cursor for the next page. Reading does not mark mail as read.
- **Write:** send a plain text email, or save one in the Drafts folder. With **Asks you first** each one is a proposal that waits for your confirmation. With **Allow all** a chat of yours sends and saves at once, and a sent mail cannot be taken back.
- **Never:** delete, move, flag, forward or expunge mail. There is no tool for it.

### Set up a connection

1. Install the **Mail** add-on from the add-on gallery.
2. Find the IMAP and SMTP server names of your provider, and, if it supports them, create an **app password** for LocalAI.
3. On the add-on page choose **New mail connection**. Enter the mailbox address, the username (often the same address), the password, and for each server its name, port and protection:

| Server | TLS (implicit) | STARTTLS |
|---|---|---|
| IMAP | port 993 | port 143 |
| SMTP | port 465 | port 587 |

   The port follows the protection you choose. Optional: a sender name, a list of the only folders the agent may use (one per line; the INBOX is matched without regard to case), and the name of the Drafts folder for a server that does not mark it.
4. Choose **Read only** (the default), **Asks you first** or **Allow all**, and save. **Allow all** lets a chat of yours send mail and save drafts without a card; everyone in To and Cc reads a sent mail, and it cannot be taken back. A run without you present still leaves a proposal. Saving logs in to both servers; a wrong password, an unreachable server or a refused certificate stops the save with a short message. **Test** logs in again and shows the IMAP result and the SMTP result on separate lines.

Example settings. These are examples: check your provider's own pages, because servers and rules change.

| Provider | IMAP | SMTP | Password |
|---|---|---|---|
| Gmail | `imap.gmail.com`, 993, TLS | `smtp.gmail.com`, 465, TLS (or 587, STARTTLS) | An app password |
| Fastmail | `imap.fastmail.com`, 993, TLS | `smtp.fastmail.com`, 465, TLS (or 587, STARTTLS) | An app password |
| Your own server (Dovecot, Postfix) | its public name, 993, TLS | its public name, 587, STARTTLS | The account password |

**Gmail.** Gmail needs an app password; the normal password does not work with IMAP in this way. Google says app passwords only work on an account with 2-Step Verification turned on, and that the option may be missing if the account uses security keys only for 2-Step Verification, is a work, school or other organization account (an administrator of a Google Workspace may block it), or has Advanced Protection on. Create one on the Google account page for app passwords. Google can change these rules and the wording; read the current page, [Sign in with app passwords](https://support.google.com/accounts/answer/185833). The username is the full address.

The saved servers, the username and the password are one sealed record. LocalAI keeps it and never shows it again, so to change any of them you tick **Change the server or login details** and enter all of them again. The mailbox address is kept in the list of the connection, and LocalAI refuses a tool call for any other mailbox.

### Security

- **The whole mailbox is reachable.** The agent can search and read every folder you allow, including mail with secrets. Use the folder list to narrow it, or connect a dedicated mailbox.
- **TLS is required by default.** The add-on checks the server certificate against the system roots and the server name; there is no option to skip the check. With STARTTLS it refuses to log in if the server does not offer the upgrade. **None** (no protection) exists only for a server you trust on a network you trust: the page warns, asks for a confirmation, and the password and all mail then travel as plain text.
- **Public servers only.** IMAP and SMTP are not HTTP, so the network check of the commands sandbox does not apply. The add-on applies the same address guard as the web fetch tool instead: ports 143, 993, 465 and 587 only (not 25), and no private, loopback, link-local, cloud metadata or LocalAI-server address. A mail server inside your own network is refused.
- **Mail is untrusted text.** Every result says it is text written by other people and must be treated as data. HTML becomes plain text with no images, links, styles or scripts; hidden and direction-changing characters are removed; attachments are never opened except as text.
- **Passwords are sealed.** They are in no result, log or proposal.
- **Writes are proposals.** A value with a line break in a header field (a recipient or a subject) is refused, so mail it read cannot add a Bcc. A send that may have reached the server is never repeated: you decide.

### Limits

A call takes at most 30 seconds. A result is at most 128 KiB; a message body is cut at 48 KiB and an attachment at 96 KiB a page, and the result says how much is left and the offset to ask for next. A mail has at most 20 recipients and a 64 KiB body. The conversation of a message is found by its Message-ID, References and In-Reply-To headers in the same folder, so mail in Sent is not part of it. Attachments of binary types return only the name, type and size. Mail is sent as plain text only, with no attachments.

## Connections that log in with OAuth

A connector add-on can declare that its connection logs you in to the service
with OAuth 2.0 instead of taking a token. You then connect the binding to your
own account at the service, and LocalAI keeps the login and renews it.

### Connect an account

On the **Accounts** tab, the group of an add-on that logs in has **Add a
connection**. Give the connection a name you recognize, such as Work or
Personal, and select **Add**. The new connection appears with **Connect**. A
person can have several connections of one add-on, each with its own name
and account. **Edit connections** opens the add-on's own page, where you
choose the resources and the write mode of each connection.

The add-on's own page can do the same. Its **Connect with Google** button (for
the Gmail and Google Drive add-ons) asks for a name, adds the connection and
sends your browser to the service at once. After you sign in, the service sends
you back to the add-on's page, not to the Accounts tab. The page lists each
connection with the same states as below (**Connect**, **Reconnect**,
**Disconnect**). While no administrator has approved the add-on's login, the
page shows no button: an administrator sees a link to the add-on's **Settings**
tab, and other people see a note to ask an administrator. A login may return to
any page of the web app that starts with `/app`: the `return_to` field of the
start call (see [the login routes]({{% relref "/addons/contract-reference/manifest-extensions#the-login-routes" %}})).

A binding that logs in shows one state under its name:

- **Not connected yet.** Tick any extra access the add-on offers under **Also
  allow**, then select **Connect**. Your browser goes to the service, where you
  sign in and agree. The service sends you back to the page you started from
  (the Accounts tab or the add-on's page), which says whether it worked. If you decline at the service, nothing changes.
- **Connected as** *account*, with the access the service granted and when the
  current token expires. LocalAI renews the token before it expires. Select
  **Disconnect** to end the login: LocalAI asks the service to revoke it and
  then forgets it. If the service does not confirm the revocation, the page
  says so; remove the access in your account at the service too.
- **Connect again.** The login stopped working, for example because the access
  was revoked at the service. The warning says why. Select **Reconnect**.
- **An administrator must approve this add-on first.** Nobody can connect until
  an administrator approves the login of the add-on. An administrator sees a
  **Review and approve** link to the form instead.

When a chat uses a connection whose login stopped working, the chat's **Tools**
menu names the connection and links to the Accounts tab.

Deleting a connection that logs in also asks the service to revoke the login
first, as **Disconnect** does. The connection is deleted even when the service
does not answer.

### Approve the login of an add-on (administrators)

Open the add-on on the **Add-ons** page and select the **Settings** tab. When
the add-on does not come from the official library, the form starts with a
warning: the approval lets it use the access tokens of everyone who connects,
so approve it only if you trust its author. The
**Account login** form shows, in plain words, where people sign in, the hosts
the login may use, the access that is always requested and the access people
may add. Before anyone can connect:

1. Register the **Callback address** with the service as the redirect address
   of a new client. If the form says the server does not know its public
   address, set the external base URL of the server first (see [OAuth 2.0
   credentials]({{% relref "/addons/contract-reference/manifest-extensions#oauth-20-credentials" %}})).
2. Enter the **Client ID** and **Client secret** that the service gives you. The
   secret is write-only: the form never shows it again, and an empty field
   keeps the stored one. A login that uses no secret has no secret field.
3. Select **Approve and save**. Saving approves the login that the form shows.
   If the add-on's login changed while the form was open, nothing is saved;
   select **Show the new login**, review it and save again. An upgrade that
   changes the login also needs a new approval. Until then the connections of
   the add-on wait and show that an administrator must approve it; they keep
   their logins. After the approval they work again without a reconnect,
   unless the add-on now sends logins to another address of the service: then
   each person connects again.

### With the API

A kind whose add-on logs in with OAuth has `"auth_kind": "oauth2"` in
`GET /api/chats/connectors/kinds`. Create its binding with
`"auth_kind": "oauth2"` and no token, then connect it. The server refuses a
token binding for such a kind, and an `oauth2` binding for a kind whose add-on
takes a token, with a `400` that says which one the add-on needs:

```bash
curl -X POST .../api/chats/connectors -H 'Content-Type: application/json' -d '{"kind": "<kind>", "name": "Work", "auth_kind": "oauth2"}'
curl -X POST .../api/chats/connectors/<id>/oauth/start -H 'Content-Type: application/json' -d '{"return_to": "/app/chats/connectors"}'
# {"authorize_url": "https://auth.example.test/authorize?...", "expires": "..."}
```

Open `authorize_url` in the same browser. After the login the service sends you
back to the page you started from, with `oauth=ok`, `oauth=denied` or
`oauth=error` and a class in the address. The binding's `oauth` object then
says `connected` and names the account. `POST .../oauth/disconnect` ends the
login. An administrator must first save the add-on's client, which approves the
login the add-on declares. See [OAuth 2.0
credentials]({{% relref "/addons/contract-reference/manifest-extensions#oauth-20-credentials" %}}) for the
routes, the callback address to register with the service and the approval.
