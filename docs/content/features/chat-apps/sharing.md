+++
url = "/features/chats-connectors/sharing/"
disableToc = false
title = "Sharing a connection"
description = "Let other accounts on the server use one of your connections in their own chats and agents."
weight = 60
devbuild = "addon"
+++

Share a connection with other accounts on the server so that they use it in their own chats and agents. Only the owner shares, from the owner's own browser session. The people you share it with never see the token.

## Sharing a connection

With accounts, the owner of a binding can share it with other people. Open
**Connectors** and choose **Share** on the binding, or on the page of the add-on
choose **Share** in the menu of its row or at the foot of its pane. The
[Share dialog]({{%relref "/features/sharing" %}}) finds people by name. A binding has
one role, **Can use**, and is never shared with everyone. A person can receive it
only when their account may use connectors (the `connectors` feature).

A person who holds **Can use**:

- sees the binding under **Shared with you**, with its name, its platform, whether it
  is on, and who shared it. Nothing else of the binding is shown.
- can enable it for the tools of its add-on in a chat, or name it in the
  connections of a saved agent. A call of those tools then uses the binding's
  credential and its list of resources, as for the owner. A shared binding is never
  picked by default: the rule "the only connection" counts the person's own bindings.
  The one exception is a saved agent that the owner shared with the person and that
  names the binding: see [Share an agent]({{%relref "/features/chats-and-agents/chats-agents#share-an-agent" %}}).
- never learns the list of resources. A call for a resource that is not on the list
  is refused and the tool result says only that it is not listed; the owner, and the
  owner's own calls, get the list so the model can pick from it.
- never reads the token or any setting. Reading, changing, switching on or off,
  testing, the sender log, pairing, deleting and sharing it again stay with the
  owner. For the person, each of these answers `404`, as for a binding that does not
  exist.

The messages that reach the bot do not change. The owner's agent answers them, as
the owner, in the owner's chats. A grantee only sends through the binding: their own
chats and agents run its add-on tools.

When the owner stops sharing, LocalAI cancels the calls of that person that are
running through the binding and waits for them before it answers. The next call is
refused like a call with a binding the person does not have. Deleting a binding
first stops every share of it.

| Method and path | Who may call it | Result |
|---|---|---|
| `GET /api/chats/connectors` | Anyone with the `connectors` feature | `connectors` are your bindings; one of a tool-only kind carries `used_by`, the number of your saved agents that use it; one you shared carries `share` with `resource_id` and `shared_with`. `shared` are the bindings shared with you, each with `id`, `name`, `kind`, `addon`, `enabled`, `active`, `owner` (a display name) and `role` (`use`). `sharing` is `false` when this server cannot share bindings. |
| `POST /api/chats/connectors/{id}/share` | The owner | `{"resource_id": "...", "shared_with": n}`: the ID of the binding in the sharing API. Another caller gets `404`; a server that cannot share answers `409` with type `sharing_disabled`. |

The grants are then listed and changed with
`/api/shares/connection/{resource_id}/grants`, see
[Share with your team]({{%relref "/features/sharing" %}}).

The shares live with the add-on owner. With several frontends, every frontend
lists the shared bindings and the shares, and makes a binding shareable, by asking
the owner. The calls through a shared binding always run on the owner, where the
tokens are, so a frontend never holds a token of a binding it does not own. When a
frontend cannot reach the owner, it lists no shared binding and no share; your own
bindings are not affected.

### Share a connection that logs in

You can share a connection that logs in, like any other connection (see
"Sharing a connection" on this page). The people you share it with use your login
through the rules of the share and nothing more:

- They use the connection in their own chats and agents, and the add-on acts
  with your account at the service, inside the folders or resources you listed.
  They never see or receive an access token, a refresh token, the client secret
  or any other part of the login.
- Only you can connect, reconnect or disconnect it. The page of a person it is
  shared with shows no login controls for it. An administrator approves the
  login of the add-on, as for every connection.
- When your login ended, or you disconnected, their chats say that the owner of
  the connection must connect it again, and the tools of the connection do not
  run. They are not sent to the service to log in. Connect it again on the
  Accounts tab and their chats work again without any change on their side.
- When you stop sharing, their use ends at once.
