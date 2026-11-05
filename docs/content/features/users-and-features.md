+++
disableToc = false
title = "Users and what each can do"
weight = 86
url = "/features/users-and-features/"
+++

LocalAI can serve several people from one server. Each person has an account,
their own files, chats, saved agents and notes, and a list of features an
administrator has switched on for them.

## Turn it on

Start LocalAI with `LOCALAI_AUTH=true`, or give it a GitHub or OIDC client
ID. The first user to sign in becomes the administrator. New users wait for
approval by default. `LOCALAI_REGISTRATION_MODE` can be `open`, `approval` or
`invite`. Set up the first administrator before you let anyone else register.
The full setup is in [Authentication and authorization]({{% relref "features/authentication" %}}).

Without authentication, there is one person and that person is the
administrator.

## What each person has of their own

With accounts on, every user has separate chats, files, saved agents,
triggers, notes (memory), pages and collections. An administrator can see the
saved agents, triggers and connector bindings of every user, switch them off
or delete them, and never sees bot tokens or an agent's instructions.

A trigger or a bot acts as its owner, with the owner's rights, quota and model
allowlist. It does not use an API key of its own.

## Features an administrator switches on

An administrator sets these in **Settings, Users and keys**. Plain chats and
the model APIs are on by default. The features that run by themselves, reach
the outside world, or run code are off.

| Feature | Default for a user | What it allows |
|---|---|---|
| `chats` | on | Chats, files, skills, notifications, your own MCP connectors |
| `agents` | off | [Saved agents]({{% relref "features/chats-agents" %}}) and chats with them |
| `triggers` | off | Schedules and file triggers: runs that start by themselves |
| `connectors` | off | Bots in Slack, Telegram and Discord bound to your agents |
| `mcp_jobs` | off | The agent tasks and jobs API |
| `pages` | off | [Pages]({{% relref "features/pages" %}}) in the workspace |
| `addons` | off | The **Add-ons** page and add-on pages such as Studio |
| `fine_tuning`, `quantization` | off | The matching tools |

An administrator always has every feature. A user whose `agents`, `triggers` or
`connectors` feature is switched off keeps the definitions; they work again
when the feature returns.

Model features such as `images`, `audio_speech` and `realtime` are on by
default and also checked by the add-on pages that use them: Studio needs
`addons` to open and `images` to make an image.

## A five-minute set-up for a team

1. Start with `LOCALAI_AUTH=true` and sign in. You are the administrator.
2. Open **Settings, Users and keys**. Create an invite link or approve a
   pending user.
3. For that user, switch on `agents` and `pages`.
4. Sign in as that user in another browser. **Agents** shows in their
   sidebar. **Pages** shows once they have a page. **Add-ons** stays hidden
   until you switch on `addons`, and the agent's triggers and bots need
   `triggers` and `connectors`.

## Limits

- A hidden sidebar item is not the protection. The server checks the
  feature on every call.
- The Add-ons page does not let a user install anything. Only an
  administrator installs add-ons. Add-ons run as trusted code with the rights
  of the LocalAI process (see [Use add-ons]({{% relref "features/using-addons" %}})).
- Turning authentication on for a server that already has data does not move
  the old data to an account. Read the migration notes first.
