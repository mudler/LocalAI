# Agent folder scope: PR description and release note draft

This file holds the draft text for the pull request and for the release notes.
Move the release note into `RELEASE_NOTES_vX.Y.Z.md` when the release is cut
(see `.agents/preparing-a-release.md`), and replace `#TBD` with the PR number.

## PR description

Saved agents: a folder instead of the whole workspace.

A saved agent can now have one folder of the user's files and a mode, read
only or read and write. A chat with such an agent starts in that folder and
cannot read, list, search, count, change or run commands against anything
outside it, through any tool, add-on, sub-agent, trigger or connector. An
agent without a folder works as before.

- The workspace stores (JSON and SQL) enforce a fence on the actor, so a
  caller that forgets the folder gets "not found", never more access.
- One resolver builds the access of a chat at every turn: the agent's folder
  at `/workspace/home`, the chat's own folder at `/workspace/chat`, the shared
  folders and skills read only.
- The file, media and search tools, the prompt, the command runners, the
  add-on workspace operations (every tool kind, bound to the chat on the
  host), sub-agents, triggers and connector replies use that access.
- A scoped agent never gets the built-in `bash` that runs on the host.
- The folder is kept by its identity: a rename or a move is followed; a
  deleted folder stops the agent with `409 agent_folder_missing`, and it never
  falls back to the home.
- REST: `folder` on the agent routes; export version 2 for an agent with a
  folder (older servers refuse it). Web interface: a folder picker in the
  agent form, chips on the agents list, the agent page and the chat, a marker
  on the Files page.
- Distributed: stateless per request; the folder is read from the stores at
  every turn and every add-on call.

Spec: `docs/superpowers/specs/2026-11-01-agent-folder-scope-design.md`.
Plan: `docs/superpowers/plans/2026-11-01-agent-folder-scope.md`.

Tests: the store fence suite runs on both stores; an end-to-end suite
(`core/services/studio/agentscope_security_test.go`) runs one scoped chat
through every surface and checks that a token in the files outside the folder
reaches no tool result, prompt, transcript, event, card, notification,
connector reply or add-on file.

Upgrade note: in a cluster, upgrade the add-on owner before the frontends. An
older owner refuses a forwarded add-on call that carries the chat binding.

## Release note

**Give a saved agent one folder.** A saved agent can now work in one folder of
your files instead of all of them, with read-only or read-and-write access.
Pick the folder in the new **Files** section of the agent form. Chats with the
agent, their sub-agents, the agent's triggers and its Telegram or Slack
connectors see only that folder and the chat's own folder; shared folders and
skills stay readable. File search, commands in the Sandbox and add-ons such as
Office are limited the same way, and the agent never gets the built-in `bash`
that runs directly on the server. Rename or move the folder and the agent
follows it; delete it and the agent stops with a clear message until you
restore it or choose another one. The REST API carries the folder as
`folder: {"path": "/Projects/Acme", "mode": "read"}`, and an agent with a
folder exports as version 2 of the agent file. (#TBD)

**Downgrade caveat.** A LocalAI version from before this release does not know
the folder of an agent: after a downgrade, agents that had a folder work on all
of the user's files again, without a warning. Export and delete such agents, or
switch off their triggers and connectors, before you downgrade. Older versions
refuse to import an agent file of version 2.

**Clusters.** Upgrade the add-on owner before the frontends: an older owner
refuses the add-on calls that a newer frontend forwards for a chat.
