+++
disableToc = false
title = "Chat"
weight = 48
url = "/features/chat/"
+++

Chat is the conversation page of the web UI. It keeps one thread per conversation, in your browser, and sends it to a chat model of your choice. The message box is the same one Home uses, so a message typed on Home opens here with the same model and the same tools.

## The thread

Your messages are the raised blocks on the right. The model's replies are plain text under its name, with a dot that is filled when the server holds the model in memory and hollow when it is not loaded yet. Code blocks have **Copy** and **Canvas** buttons. Images you attach appear as thumbnails that open in a viewer, and other files appear as chips.

Reasoning and tool calls fold into one quiet line (for example "Thought · read_file"). Click it to open the steps with their arguments and results. While a reply is arriving the line is open and shimmers, and it folds when the answer starts.

If a reply fails, the text written so far is kept, the reason is shown, and **Retry** asks again from your last message. **View traces** opens the backend traces.

### Actions on a message

Hover a message, focus it, or look at the last one (on a phone every message shows them): **Copy**, **Edit**, **Regenerate** and **Branch from here**. Edit saves the new text in place and does not send anything. Regenerate drops the answer and everything after it and asks again. Branch starts a new chat with the history up to that answer.

| Key | Action |
|---|---|
| `Up`, `Down` | Move between messages (a message must have focus) |
| `C`, `E`, `R`, `B` | Copy, edit, regenerate or branch the focused message |
| `Up` in an empty box | Edit your last message |
| `Esc` | Stop the reply that is streaming; then close the search; then close the canvas |

## The message box

The model chip lists the chat models. **Loaded now** holds the models in memory, **Installed** the rest. When the list opens, LocalAI reads the host memory once and asks the server to estimate each listed model at this chat's context size (up to twelve models). A row shows what the model needs and whether it fits: free memory, how much would run on the CPU, or how far over the machine it is. A model with no estimate shows no fit text. The server reports no load time, so none is shown.

The **MCP** chip opens the server and client tool lists. **Canvas** turns code blocks into cards that open in a side panel. Type `/` for the actions the page has:

| Action | What it does |
|---|---|
| `/model` | Open the model list |
| `/new` | Start an empty chat on the same model |
| `/chats` | Open the conversations list |
| `/assistant` | Turn Manage mode on or off (admin only) |
| `/canvas` | Turn Canvas on or off |
| `/find` | Search this chat |
| `/settings` | Open the chat settings |
| `/export` | Download the chat as Markdown |
| `/clear` | Remove every message, after a confirmation |

Enter sends, Shift+Enter adds a line. Paste an image to attach it. Under the box, the line shows the speed while a reply streams and the token count of the chat.

## Conversations

Press `Ctrl+K` (`Cmd+K`) or **Chats** to open the list of conversations, grouped by day like **Jump back in** on Home. Type to search names and message text. Arrow keys move, `Enter` opens, `F2` renames and `Delete` removes. Each row also offers rename, duplicate, copy and export. Removing a chat hides it and shows an **Undo** toast; the chat is deleted for good when the toast goes away. The name in the header can be renamed with a click.

The history is stored in this browser (`localai_chats_data`), not on the server.

## Settings

The sliders button opens the chat settings. They apply from the next message.

- **System prompt.** Sent before every message in this chat. Empty means the model's own default.
- **Sampling.** Temperature, top P and top K. Each says "model default" until you change it, and has a **Reset**.
- **Context window.** The size drives the meter in the header and the warning when the context is nearly full. It is not sent to the model. Admins get it filled in from the model's configuration.
- **Behaviour.** Manage mode (admin) and Focus mode, which collapses the app sidebar while a conversation is open.
- **Model info** (admin). Backend, model file, context size, threads, GPU layers and an **Edit config** button.

## Find, canvas and long threads

`Ctrl+Shift+F` (`Cmd+Shift+F`) searches this chat: it marks the matches in the messages that are loaded in the page, shows "n of m" and steps with `Enter` and `Shift+Enter`. Nothing is sent to the server. When you scroll away from the end of a long thread, **Jump to latest** brings you back.

With Canvas on, code blocks become cards. The canvas panel opens beside the thread with tabs, a Code and Preview switch for HTML, SVG and Markdown, Copy and Download. On a narrow window it takes the whole page.

## When there is nothing to chat with

An empty chat shows a few starters, whether the model is loaded and your recent conversations. With no chat model installed, the page offers the starter models for the hardware, the gallery and import, and keeps what you typed. While a model is loading on a worker or being staged, the reply waits behind a card that names the phase the server reports, the node, the bytes and the time left when the server gives them, and then sends by itself. Stop cancels the wait.
