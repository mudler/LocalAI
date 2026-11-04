# Home is the front door; Chats leaves the sidebar

Date: 2026-10-30. Status: decided by the owner, implemented on `feat/home-front-door`.

## Amendments (2026-10-31)

Later work changed two points of the text below; the code and
`docs/content/` describe what shipped.

- **Home starts the chat itself.** Home's composer creates the chat and
  sends the first message, then opens `/app/chats/:id`. It does not open
  `/app/chats?model=…` with the message in the navigation state any more
  (flow 1). The chat page draws the message from a short-lived hand-off
  (`utils/composerHandoff.js`: the chat record, the text and the files) and
  the composer glides to the chat's dock. The hand-offs of flow 6 still use
  navigation state.
- **Home's composer takes attachments.** It is the chat page's composer
  (`ChatComposer`, `useChatComposer`): files, the agent picker, the Tools and
  Skills menus, paste to attach. "Attachments on Home's composer" is no
  longer out of scope.

## Problem

Home and the Chats page are two doors to the same room. The Home composer
starts a chat, and after the first message the person is on the transcript
at `/app/chats/:id`. The sidebar entry **Chats** opened `/app/chats`, an
empty chat with its own composer, model picker and "jump back in" list: a
second Home. People had two places to start a chat and no place to manage
their chat history except the Ctrl+K menu inside a transcript.

## Decision

- Home (`/app`) is the front door. Its composer, resume strip, memory strip,
  first-run card, widgets, shortcuts and example cards do not change.
- The sidebar has no **Chats** entry. In its place, under the primary entries,
  the sidebar lists the most recent chats, with a **New chat** action (it goes
  to Home) and an **All chats** link.
- The transcript route `/app/chats/:id` does not change.
- `/app/chats` with no id, opened as an address (a bookmark, an old link, a
  reload, `?model=`), goes to Home. `?model=` travels to Home and selects that
  model in the picker.
- `/app/chats/all` is the new **All chats** view: the chat history with
  search, rename, copy, export, delete with Undo, Clear all and Import. It
  reuses the list of the Ctrl+K menu (the list moved into `ChatsList`; the menu
  and the page both render it).
- Agents, skills, triggers, connectors, Files, Pages keep their entries. The
  feature gates (`hasFeature('chats')`, `/api/features`) do not change.

## User flows

1. **Start a chat.** Home → type → Send. Home creates the chat, sends the
   message and opens `/app/chats/:id` (see the amendments; the first version
   opened `/app/chats?model=…` with the message in the navigation state).
2. **Resume a chat.** Home "Jump back in", or a row of the sidebar's recent
   chats, or a row of All chats, or Ctrl+K inside a transcript.
3. **New chat.** The sidebar's **New chat** (a `+` beside "Recent chats"), the
   transcript's More → New chat, `/new` in a transcript and New chat in the
   Ctrl+K menu all go to Home. `/new` on Home keeps its behaviour: an empty
   chat on the chosen model.
4. **Find or clean up a chat.** Sidebar → **All chats**, or Home → **All
   chats** beside "Jump back in". Search matches titles at once and message
   text from the server. Delete hides the row and offers Undo for 6 s; the
   server deletes it when the offer ends.
5. **Old links.** `/app/chats` → `/app`. `/app/chats?model=x` → `/app?model=x`.
   `/app/chat` and `/app/chat/<model>` go to Home in one step.
6. **Hand-offs that open an empty chat.** A page that hands text or an agent
   to a new chat (Agents → Chat, Pages → "Ask", a widget's "Fix", Home
   `/new`) navigates to `/app/chats` with navigation state. That still opens
   the empty transcript surface, because it carries something Home cannot
   take (an agent, a draft for the chat's composer).

## Decisions in detail

- **Redirect rule.** `/app/chats` renders the chat page only when the
  navigation carries state (a hand-off from inside the app). Without state it
  is an address, and it redirects to `/app`, keeping the query. The chat page
  replaces its consumed state with a `{ newChat: true }` marker instead of
  `null`, so clearing a draft or a start message does not bounce the page to
  Home.
- **Deleting the open chat** in a transcript stays on the empty chat surface
  with the Undo toast, as before. Leaving the page would end the offer at
  once, because the page commits a pending delete when it unmounts.
- **Recent chats in the sidebar.** At most 8, newest first. The list is read
  from `GET /api/chats?limit=8&kind=chat` when the sidebar mounts and on each
  route change. The chat page and All chats announce their list after a
  rename, delete or Undo, so the sidebar follows without another request. A
  row is a link: Tab reaches it, Enter opens it, the current chat is marked
  `aria-current="page"`.
- **Collapsed rail.** The recent rows hide (titles do not fit in 56 px). An
  **All chats** icon stays, so the history is one click away while a
  transcript's focus mode collapses the rail.
- **Phone drawer.** The same list as the expanded sidebar; a click closes the
  drawer.
- **Legacy sidebar** (`addon_navigation: false`) and the 404 page list **All
  chats** where they listed **Chats**.
- **One list, two frames.** `ChatsList` holds the search, the day groups, the
  rows, the row actions and the keyboard (↑ ↓ Enter F2 Del). `ChatsMenu` puts
  it in the Ctrl+K dialog; All chats puts it on the page. The delete-with-Undo
  logic moved from the chat page into `useChatDelete`, which both use.

## Out of scope

- Pinned and archived chats: the server has neither today, and the current
  list has neither.
- ~~Attachments on Home's composer.~~ Shipped later, see the amendments.
