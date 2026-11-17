+++
url = "/features/chats/"
disableToc = false
title = "Chats"
description = "Start a chat, give it files and goals, repeat prompts, hand work to sub-agents and share what it makes."
weight = 10
devbuild = "feature"
aliases = ["/features/agents/"]
+++

Chats are conversations that the server keeps. You can keep typing while the
model works, the model can hand work to sub-agents, long conversations are
compacted for you, and the model can use the MCP servers of its model
configuration.

The chats API sits beside the OpenAI-compatible `/v1/chat/completions` endpoint,
which does not change. Use `/v1` when a client brings its own history and needs
the OpenAI format. Use `/api/chats` when the server should keep the
conversation.

{{% callout style="note" %}}
Chats need the `chats` feature. It is on for every user by
default. An administrator can switch it off for one user in the user's
permissions. Saved agents, triggers, connectors and agent jobs are separate
features that are off by default; see [Permissions]({{% relref "/administration/chats-deployment#permissions" %}}).
{{% /callout %}}

## What you can do

In the order you will probably need it:

- **Start a chat and send messages.** Pick a model on Home and type. See [Chats in the web UI](#chats-in-the-web-ui) and [The chat page](#the-chat-page). From code, use the [Chats API]({{% relref "/features/chats-and-agents/chats-api" %}}).
- **Give it files.** Attach files to a message, or let the model work in your files. See [The files of a chat]({{% relref "/features/chats-and-agents/chats-files" %}}) and the [Files API and reference]({{% relref "/features/files-and-documents/files-reference" %}}).
- **Set a goal.** The chat keeps working toward it without you typing "continue". See [Goals]({{% relref "/features/chats-and-agents/chats-goals" %}}).
- **Repeat a prompt.** Run it on a timer with a loop. See [Loops]({{% relref "/features/chats-and-agents/chats-loops" %}}).
- **Hand work to sub-agents.** See [Sub-agents]({{% relref "/features/chats-and-agents/chats-sub-agents" %}}).
- **Run commands.** See [Running commands in a chat]({{% relref "/features/chats-and-agents/chats-commands" %}}).
- **Keep a long chat going.** LocalAI compacts it for you. See [Long conversations]({{% relref "/features/chats-and-agents/chats-long-conversations" %}}).
- **Use skills, MCP tools and the web.** See [Slash commands and skills]({{% relref "/features/chats-and-agents/chats-skills" %}}) and [Tools in a chat]({{% relref "/features/chats-and-agents/chats-tools" %}}).
- **Share.** A chat belongs to you. To share work, share a folder of your files or a saved agent: see [Share a folder of your home]({{% relref "/features/files-and-documents/files-reference#share-a-folder-of-your-home" %}}) and [Sharing]({{% relref "/features/sharing" %}}).
- **Run chats for a team.** Administrators grant features, choose storage and run a cluster: see [Chats on a server]({{% relref "/administration/chats-deployment" %}}).

## Chats in the web UI

The web UI talks to this API, so a chat keeps running when you close the tab
and opens again from any browser.

- **Home starts a chat.** Home (`/app`) is the front door. Its message box is
  the one of the chat page, so everything a new chat offers works there before
  the chat exists:
  - pick the model (each row says whether it is loaded, what it can do and how
    it fits this machine) and a [saved agent]({{% relref "/features/chats-and-agents/chats-agents" %}});
  - switch tools, add-ons and skills in the **Tools** and **Skills** menus.
    They are a draft: the chat starts with them (`switches` in
    `POST /api/chats`);
  - attach images, audio and other files with the buttons, or paste an image.
    They go with the first message;
  - dictate, use the shortcuts row above the box, and type `/` for the Home
    actions;
  - `Enter` sends and `Shift+Enter` adds a line.

  The text you type on Home stays in the browser across a reload until you
  send it (staged files stay only until the page closes). **Send** creates the
  chat, sends the message with its files and opens the chat at
  `/app/chats/<id>`. If the message cannot be sent, the new chat is removed and
  the text and files stay on Home. Home also lists your newest chats under
  **Jump back in**.
- **The sidebar** lists your most recent chats under the main entries, with
  **New chat** (the `+` beside **Recent chats**, which opens Home) and **All
  chats**. When the sidebar is collapsed, only the **All chats** icon shows.
  There is no separate Chats entry: Home is where a chat starts.
- **All chats** (`/app/chats/all`) is your chat history: open it from the
  sidebar or from **All chats** next to **Jump back in** on Home. Type to find
  a chat by its name or by what was said in it (the server search); the line
  under a match shows where it matched. Open, rename (`F2`), copy, export as
  Markdown or delete one, clear them all, or import a JSON export. A deleted
  chat can be brought back for a few seconds with **Undo**; then the server
  deletes it.

  The page lists the chats you started. Chats that something else started (a
  connector, a trigger, an agent job or a page) have a second tab, **From
  add-ons and automations**, which appears once there is one. It groups them by
  what started them: first the kind (triggers, connectors, jobs, pages, any
  other), then the origin itself, such as one connector binding or one
  trigger, with the origin's name, how many chats it has, when it was last
  active, a **Running** mark while a turn runs and **Waiting for you** when a
  chat needs you (its turn paused at the step limit, or an unread inbox item
  asks for you). A link opens the origin's page. These chats do not appear in
  the sidebar, in **Jump back in** or in the **My chats** tab; the **All
  chats** entry in the sidebar shows a number when some of them wait for you.
  `Ctrl+K` in a transcript still lists and finds every chat, with a badge on
  the ones you did not start. Opening one shows "This conversation was started
  by ..." over the transcript, with a link to the origin. The browser
  remembers which tab you used last.

## The chat page

The chat page (`/app/chats/<id>`) shows one chat.

- **Conversations.** `Ctrl+K` (`Cmd+K` on a Mac), or **Chats** in the header,
  opens the same list as **All chats** over the chat, to move to another chat
  without leaving the page.
- **A new chat.** **New chat** in the **More** menu, in the `Ctrl+K` list and
  `/new` in the message box open Home. A chat with a [saved
  agent]({{% relref "/features/chats-and-agents/chats-agents" %}}) starts from the agent's row on the Agents
  page: the chat page opens empty with the agent chosen, and its **Tools** and
  **Skills** menus are a draft for the new chat, as on Home. Picking another model
  in an open chat moves the chat to it from the next turn (not while a turn
  runs). `/assistant` starts a chat with the LocalAI management tools
  (administrators, when the assistant is on).
- **Chat settings** (the sliders icon in the header) holds the [settings]({{% relref "/features/chats-and-agents/chats-api#settings-rename-and-model-change" %}}):
  a system prompt, temperature, top P, top K, context size and reasoning
  effort. **Save settings** sends them all; they apply from the next turn.
- **While the model works** you can keep typing and attach files. A message you
  send waits over the message box and reaches the model at its next step;
  edit or cancel it until then. `Esc` or **Stop** ends the turn; waiting
  messages stay for **Send now**. `Up` in an empty box edits the last waiting
  message, or puts your last message back in the box.
- **Drafts stay with their chat.** What you type or attach in a chat stays
  there when you open another chat, and comes back when you return; a new chat
  starts empty. The browser keeps the text of each draft (not the files)
  across a reload, and drops it when you send it or delete the chat. A waiting
  message always goes to the chat you wrote it in, also when you open another
  chat before the server answers.
- **On a message**: **Edit** (your message) replaces it and runs the chat
  again from it, **Regenerate** (the last answer) answers again, and
  **Branch** copies the chat up to that message into a new chat. The keys are
  `E`, `R` and `B` on a focused message.
- **More** exports the chat as Markdown or JSON and imports a JSON export as a
  new chat.
- **Under the message box** the foot shows the context meter and the speed of
  the last model request (from the `usage` events). It turns amber above 80
  percent of the window and red above 90.
- **Tools, Skills and Sub-agents** menus over the message box switch tools and
  skills for this chat (see [Tools in a chat]({{% relref "/features/chats-and-agents/chats-tools#switch-tools-off-for-one-chat" %}}); a skill is switched by its `id`, and the Skills menu has Select all, Disable all and a search), and list
  the sub-agents of the chat with their transcript and a **Stop** for a
  running one. The Tools menu says whether the Sandbox add-on runs commands
  isolated, directly on this machine, or not at all. When the chat cannot run
  commands, the Tools menu opens with a notice, "Commands can't run here", and
  the reason: no add-on that runs code is installed, the Sandbox is off for
  this chat, or it cannot run on this server. An administrator gets a link to
  install the Sandbox or to its add-on page; anyone else is told to ask an
  administrator. A chip with the same words sits next to the Tools menu (on a
  phone, only its icon) and opens the notice. **Hide** removes the chip and
  leaves one line in the Tools menu; this browser remembers it until the
  reason changes. The Tools menu shows for this notice even when there is
  nothing to switch. Changes an add-on tool
  made show under the tool line with **Undo** when the add-on can undo them; a
  call the add-on refused (`not_permitted`, `resource_unreachable`,
  `not_running`, `invalid_request`) says why. MCP Apps show live under their
  tool call.
- **The preview pane** on the right shows what the assistant made. It opens
  by itself when a turn creates or changes a file, or when an answer holds a
  result: a whole HTML page, an SVG, a code block of 25 lines or more, or a
  block the answer presents as a file. A short snippet never opens it. It
  opens beside the thread from 1280 px and over it below that; on a phone it
  never opens by itself. It does not take the focus from the message box, and
  a screen reader hears "Preview opened: budget.xlsx". Close it and it stays
  closed in that chat (this browser remembers it) until you open it again.
- **Chips under an answer** name the files its turn made: "Created
  budget.xlsx · Open preview" or "Changed ...". A chip opens the pane on its
  file. **Files** over the message box opens and closes the pane.
- **What the pane shows.** The selected file or code block on top, with
  **Download**, **Copy** (text and code) and **Open in Files**, then the
  chat's files in two groups, **Created and changed** and **You gave it**,
  each with its size and turn. Spreadsheets, CSV and TSV files show as a table
  with a tab per sheet and the first 200 rows; Word documents and
  presentations as their text (the formatting is not shown); PDF files in the
  browser's viewer; HTML files run in the sandbox of
  [Pages]({{%relref "/features/pages-and-home/pages" %}}) with no network and no bridge;
  images, Markdown, JSON, code, audio and video with their own views; any
  other type as a card with **Download**. An add-on that previews a type (the
  `files.preview` slot) replaces the built-in view for it. Drop files on the
  pane to attach them to your next message. A file the model received as an
  image or video is marked **In context**.
- **Code blocks** in an answer have **Copy** and **Download**, and a block
  that is a result has **Open preview**.
- **A trigger the model proposes** shows as a card under its tool line with
  **Create trigger**, **Edit** (name and schedule or folder) and **Dismiss**.
- **A change of a trigger the model asks for** shows as a card under its tool
  line: an edit lists each field before and after with **Apply** and
  **Dismiss**, a delete shows the trigger with **Delete** and **Keep**, and a
  pause or a resume, which applies at once, is a receipt with **Undo**. See
  [Changes from a chat]({{% relref "/features/chats-and-agents/chats-agents" %}}#changes-from-a-chat).
- **Shortcuts** over the message box are the prompts of your
  `LocalAI/shortcuts.json`, or the default starters without it. A click puts one in the
  box; **Edit shortcuts** changes the list. See [Make LocalAI
  yours]({{% relref "/features/chats-and-agents/make-localai-yours" %}}).
- **Old addresses.** `/app/chats` with no chat id opens Home. A `?model=` in
  the address selects that model on Home, so `/app/chats?model=<id>` and the
  older `/app/chat/<model>` still start a chat on that model.
- **Find** (`Ctrl+Shift+F`), focus mode, copying the chat as Markdown and
  the keyboard shortcuts are all available.

Administrators find every user's agents, triggers and connectors under
**Admin → Settings → Chats of all users**. The [agent tasks and
jobs]({{% relref "/features/chats-and-agents/agent-jobs" %}}) have a page of their own, **Agents > Tasks**
(`/app/agent-tasks`).

## Troubleshooting a failed reply

When a turn fails, the thread shows one banner: "The reply failed", the reason in plain words, what to do about it, a **Retry** button and, under **Technical detail**, the error text with addresses, paths and keys removed. The full cause is in the server log. The banner names the cause when LocalAI can tell:

| Reason in the banner | What it means | What to do |
|---|---|---|
| The model "x" could not be started: its backend is not installed | The model needs a backend that is not installed. | Install the backend from the Backends page (the banner links to it for administrators), or pick another model. |
| The model "x" failed to start a moment ago and is resting | A load failed and LocalAI waits before it tries again. | Try again after the number of seconds the banner gives. |
| The model "x" could not be started | The backend started but the model did not load. | Check the model's configuration and the backend log. |
| The model "x" was not found | No model has that name. | Pick another model, or install it from the Models page. |
| The upstream refused the key | A cloud-proxy model's provider answered 401 or 403. | Check the API key in the model's settings. |
| The upstream does not accept this request at that address | The provider answered 404 or 405. | Set `upstream_url` to the full endpoint, for example `.../v1/chat/completions`. |
| The upstream address could not be reached | The host name does not resolve, or nothing answers there. | Check the address in the model's settings. |
| The provider behind the model returned an error | The provider or a proxy answered with a server error several times in a row. | Retry in a moment, then check the provider. |
| The model took too long to answer / The turn was cancelled | A timeout, or the turn was stopped. | Retry, or use a smaller or faster model. |

The banner stands after the messages, tool calls and results that the turn produced before it failed: nothing of them is removed. **Retry** goes on from where the turn stopped (`POST /api/chats/{id}/resume`). The model keeps its own state, so a tool call that already has a result is not made again, a step that did not finish is done again, the messages you sent while the turn ran stay, and a summary made by a compaction stays as it is. A turn that a restart of the server interrupted can be resumed the same way. With `--chats-auto-resume` the server does it, and the card says that the server is going on by itself while it waits. The chat shows such a turn as **The reply was interrupted**, keeps everything written so far, and resumes it by itself after a short wait: up to 3 tries, with a wait that doubles from 3 seconds up to 30 seconds. **Stop trying** cancels the wait, and **Retry** resumes at once and starts the count again. Only an interrupted turn is resumed this way; a turn that failed (a model error, a refused key) waits for you, and so does an interruption older than 10 minutes. When the connection to the server is lost for more than two seconds, the chat says "Connection lost. Reconnecting." until it is back. A second click, or a click in another window, starts nothing. To answer your last message again from the start, use **Regenerate** on the message.

The reason is stored with the chat, so a reload shows the same banner as the live page, for a day after the turn. A new turn clears the banner.

## Who can see a chat

A chat belongs to the user who started it. Another user's chat id always
answers `404`. Requests made on the user's behalf go through the same
model access rules and quotas as a direct request to `/v1/chat/completions`.

With authentication off, chats belong to one anonymous owner.

## Memory settings

Your settings page (Account) has a **Memory** tab. It lists the notes that the host
puts in the prompt of your chats ([Memory]({{% relref "/features/chats-and-agents/short-memory" %}}) is built in, and another add-on can declare context files): your own index and its topic files, the notes of
each saved agent and of each connector conversation, and the agent profiles. Each
index shows its size against the caps (at most 40 lines and 2 KiB for an index,
4 KiB for a profile, 6 KiB of notes in one turn). You can open a file, edit it,
delete it (it goes to the Trash) or clear all of them. An index cannot be saved
over its cap. A model changes these files only through a confirmed
[change proposal]({{% relref "/addons/contract-reference/connectors-and-chats#change-proposals-from-chats" %}}).

`GET /api/chats/context-files` returns the list for the page: `limits`, and
`groups` of `{source, scope, key, max_bytes, max_lines, files[]}`. It only reads.
The page edits and deletes with the Files API.

## Saved agents

An agent is a saved set of instructions and tool grants that a chat can start
with. See [Saved agents, triggers and inbox]({{% relref "/features/chats-and-agents/chats-agents" %}}).

## The LocalAI assistant

An admin can start a chat that has the tools to manage the instance (install
models and backends, edit model configs, check status). It starts from the Home
page with `/assistant`, or over the API with `assistant: true` in
`POST /api/chats`. It is an ordinary chat in the list after that. Only that chat
has the tools, never its sub-agents. The `chats_localai_admin` setting
turns the admin tools off. See [LocalAI Assistant]({{% relref "/features/chats-and-agents/localai-assistant" %}}).

## Moving from the old chat

The old chat page, the agents, the agent jobs and the old skills were replaced by the chats. At the first start the server converts what they left; see [Moving from the old chat and agents]({{% relref "/features/chats-migration" %}}).

<script>
var m={"permissions":"../../administration/chats-deployment/#permissions","what-an-administrator-can-see":"../../administration/chats-deployment/#what-an-administrator-can-see","switch-the-chats-off":"../../administration/chats-deployment/#switch-the-chats-off","start-a-chat-and-send-messages":"../chats-api/#start-a-chat-and-send-messages","start-a-chat-with-its-switches":"../chats-api/#start-a-chat-with-its-switches","list-the-chats-of-one-origin":"../chats-api/#list-the-chats-of-one-origin","keep-typing-while-the-model-works":"../chats-api/#keep-typing-while-the-model-works","talk-to-a-chat-live":"../chats-api/#talk-to-a-chat-live","loops-repeat-a-prompt-in-a-chat":"../chats-loops/#loops-repeat-a-prompt-in-a-chat","loop-or-trigger":"../chats-loops/#loop-or-trigger","settings-rename-and-model-change":"../chats-api/#settings-rename-and-model-change","context-meter-and-speed":"../chats-api/#context-meter-and-speed","the-status-line":"../chats-api/#the-status-line","goals":"../chats-goals/#goals","regenerate-edit-fork-search-export":"../chats-api/#regenerate-edit-fork-search-export","search-your-chats":"../chats-api/#search-your-chats","images-video-and-documents":"../chats-api/#images-video-and-documents","images-and-vision":"../chats-api/#images-and-vision","the-files-of-a-chat":"../chats-files/#the-files-of-a-chat","files-that-a-command-saved":"../chats-files/#files-that-a-command-saved","working-files-and-the-work-folder":"../chats-files/#working-files-and-the-work-folder","sub-agents":"../chats-sub-agents/#sub-agents","jobs-and-stall-waits-of-sub-agents":"../chats-sub-agents/#jobs-and-stall-waits-of-sub-agents","saved-agents-as-sub-agents":"../chats-sub-agents/#saved-agents-as-sub-agents","past-chats":"../chats-sub-agents/#past-chats","running-commands":"../chats-commands/#running-commands","background-jobs":"../chats-commands/#background-jobs","network-of-the-sandbox-commands":"../chats-commands/#network-of-the-sandbox-commands","sandbox-profile":"../chats-commands/#sandbox-profile","commands-on-this-machine":"../chats-commands/#commands-on-this-machine","what-the-web-interface-is-told":"../chats-commands/#what-the-web-interface-is-told","the-connections-of-an-add-on":"../chats-commands/#the-connections-of-an-add-on","long-conversations":"../chats-long-conversations/#long-conversations","reasoning-of-earlier-turns":"../chats-long-conversations/#reasoning-of-earlier-turns","the-context-window-a-chat-needs":"../chats-long-conversations/#the-context-window-a-chat-needs","compact-or-clear-the-context-yourself":"../chats-long-conversations/#compact-or-clear-the-context-yourself","messages-sent-with-a-skill-or-a-saved-agent":"../chats-skills/#messages-sent-with-a-skill-or-a-saved-agent","slash-commands":"../chats-skills/#slash-commands","run-a-skill":"../chats-skills/#run-a-skill","hand-a-task-to-a-saved-agent":"../chats-skills/#hand-a-task-to-a-saved-agent","mcp-tools":"../chats-tools/#mcp-tools","prompts-and-resources-as-context":"../chats-tools/#prompts-and-resources-as-context","switch-tools-off-for-one-chat":"../chats-tools/#switch-tools-off-for-one-chat","interactive-tool-pages-mcp-apps":"../chats-tools/#interactive-tool-pages-mcp-apps","skills":"../chats-skills/#skills","import-and-export-skills":"../chats-skills/#import-and-export-skills","document-skills":"../chats-skills/#document-skills","web":"../chats-tools/#web","collections":"../chats-tools/#collections","add-on-tools":"../chats-tools/#add-on-tools","metrics":"../chats-api/#metrics","your-files":"../files-reference/#your-files","search-inside-documents":"../files-reference/#search-inside-documents","the-files-page":"../files-reference/#the-files-page","extract-an-archive":"../files-reference/#extract-an-archive","7z-and-rar-risk":"../files-reference/#7z-and-rar-risk","your-own-rules-agentsmd":"../files-reference/#your-own-rules-agentsmd","shared-folders":"../files-reference/#shared-folders","share-a-folder-of-your-home":"../files-reference/#share-a-folder-of-your-home","storage":"../../administration/chats-deployment/#storage","clusters":"../chats-agents/#clusters","api-reference":"../chats-api/#api-reference","shared-filesystem-for-files":"../../administration/chats-deployment/#shared-filesystem-for-files"};var h=decodeURIComponent((location.hash||"").slice(1));if(h&&m[h]&&!document.getElementById(h)){location.replace(m[h]);}
</script>
