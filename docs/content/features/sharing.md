+++
url = "/features/sharing/"
disableToc = false
title = "Share with your team"
weight = 40
description = "Share folders, saved agents, collections and connections with other users, and choose what they may do."
keywords = ["team","sharing","permissions","users"]
+++

LocalAI keeps one record of who may use a shared resource. Add-on resources,
such as a [Tables]({{%relref "/addons/catalogue/work/tables" %}}) database, are shared through
the add-on routes. Other kinds of resources are shared through the routes on this
page, with the same rules. A kind is a type of resource that a LocalAI service
registers with the host, for example `example:note`.

Sharing is never a tool. No MCP tool, model tool, agent or proposal can share,
lower or revoke anything. The routes that change a grant accept only the owner's
own browser session.

## Roles

Each resource has one owner, who created it. The owner never changes and is never
a grant. The owner may do everything with the resource, including sharing and
deleting it.

The other people hold a grant. A grant gives one role on the whole resource. Each
kind declares which roles it grants and, for each action, which roles may run it:

| Kind | Roles | What a role may do |
|------|-------|--------------------|
| Add-on resources | `viewer`, `editor` | `viewer` reads. `editor` reads and writes rows. Only the owner changes the schema, shares or deletes. |
| Saved agents (`agent`) | `use`, `viewer` | `use` chats with the agent as yourself. `viewer` also reads the definition. Only the owner edits, deletes, exports or shares. See [Share an agent]({{%relref "/features/chats-and-agents/chats-agents#share-an-agent" %}}). |
| Other kinds | `use`, `manage` (or the subset the kind declares) | The kind decides. Usually `use` reads and runs the resource and `manage` also changes it. |
| Folders of your home | `read`, `write` | `read` reads the folder and everything in it. `write` also changes and deletes there. Only the owner shares. See [Share a folder of your home]({{%relref "/features/chats-and-agents/chats#share-a-folder-of-your-home" %}}). |

A role is a set of actions, not a rank. LocalAI refuses to register a kind whose
table names a role that the kind does not grant. A role that is stored but not
listed for an action is denied, and so is any role that LocalAI does not know.

A kind also decides who may receive a grant. LocalAI asks that rule only about
active named accounts, so a disabled or unknown account never receives a grant. A
kind without the rule cannot be shared with a person at all. The add-on kinds keep
their own rule: the person must be allowed to use add-ons.

## Features

A kind can name the feature (permission) that a person needs to use its resources,
for example a feature that turns a part of LocalAI on or off per account. LocalAI
checks it with the account on every call for a resource of the kind: listing,
sharing, and the service's own work under the grant. A kind that names no feature
needs only an active named account with a valid credential.

Sharing a kind does not need the `addons` feature, unless the kind names it. Only
the add-on resources and their routes under `/api/addons` need it.

## Sharing with everyone

A kind can allow the principal `@everyone`. A grant to `@everyone` reaches every
active named account, including accounts created after the grant. It is stored as
one grant. No account can sign in as `@everyone`.

A person can hold a grant of their own and the grant to `@everyone`. The two add
up: the person may run every action that one of them allows. Each one is changed
and revoked by itself. Revoking `@everyone` leaves the grants of named people, and
revoking a person leaves what `@everyone` holds.

## Lowering and revoking

When the owner lowers or removes a grant, LocalAI:

1. records the change and starts a new authority epoch for the resource,
2. cancels the admitted work of the grantee whose action the new role no longer
   allows, for `@everyone` the work of every person that their own grant does not
   allow,
3. waits for that work to stop, and only then answers.

The cancel looks at what each piece of work does. Lowering an agent from `viewer`
to `use` stops a read of the definition and keeps a running chat, because `use`
still chats with the agent. Removing the grant stops both.

If the work does not stop within 10 seconds, the answer is a 503 and the change is
already in force for new work. Repeat the same call. LocalAI keeps the change in its
revocation log, so restoring an older backup does not give the access back.

There are two revocation logs of up to 4096 changes each: one for the add-on
resources and one for the other kinds. Changes on agents, bindings and collections
therefore never push a change of an add-on resource out of its log. A restore of an
add-on reads only the add-on log. When that log is full, its oldest changes go, and
a restore from a backup older than them gives back the data and no access at all.

## Limits

A shared resource of a kind that is not an add-on has one record in the policy
store, made when its owner shares it the first time. These records have their own
limits, apart from the add-on resources:

| Records | Per owner | In total |
|---------|-----------|----------|
| Add-on resources | 256 | 4096 |
| Share records (agents, bindings, collections, other kinds) | 256 | 8192 |

An owner at the limit of share records cannot share one more resource for the first
time until one of their records goes; resources that are already shared are not
affected. Share records never take the room of add-on resources, and an owner reaches
their own limit long before the total: 32 owners at their limit are needed to fill it.

A record is dropped when its resource is deleted: deleting a saved agent or a
binding revokes every grant and then drops the record. At startup the add-on owner
also checks the share records of every kind against the kind's own check that the
resource still exists, and drops the records of agents that are gone, for example
an agent deleted while the owner could not be reached. A check that cannot answer
keeps the record for the next start. A collection has no delete route; resetting a
collection keeps its record and its grants.

## API

All routes need an authenticated named account. They do not need the `addons`
feature; a kind that names a feature needs that one (see [Features](#features)).
The grants live in the policy store of the add-on owner (see
[Addon contracts]({{%relref "/addons/contract-reference" %}})), so the routes exist only while
the add-on owner runs (`--addons-enabled`, on by default), and a frontend of a
distributed deployment forwards each call to the owner.

| Method and path | Who may call it |
|-----------------|-----------------|
| `GET /api/shares/{kind}` | Anyone. API keys may call it. |
| `GET /api/shares/{kind}/{resourceId}/grants` | The owner, from the owner's browser session |
| `PUT /api/shares/{kind}/{resourceId}/grants/{principal}` | The owner, from the owner's browser session |
| `DELETE /api/shares/{kind}/{resourceId}/grants/{principal}` | The owner, from the owner's browser session |
| `GET /api/shares/{kind}/{resourceId}/users?q=` | The owner, from the owner's browser session |

A browser session call must be a same-origin `fetch`, carry the header
`X-LocalAI-Confirm` with the resource ID, and carry no API key and no tool, agent or
MCP marker. Any other call gets 403.

`{principal}` is the account ID that the user picker returned, or `@everyone`.

The user picker (`GET .../users?q=`) searches every active named account by a prefix
of the display name, 3 to 64 characters, and keeps only the accounts that the kind
accepts. A prefix of an address finds nobody, so the picker does not tell who has an
account. An account without a display name is found only by its whole address and is
returned with an empty display name; the Share dialog shows it as an unnamed person.
The picker returns at most 20 people, each with the account ID and the display name
only, and never the owner or the caller.

Each person may search 20 times at once and then once every 2 seconds. A search over
that rate gets 429 with type `rate_limited`. For the kinds on this page, the add-on
owner, the only process that answers the picker, keeps the count per person in memory,
so the rate holds for the whole cluster and a restart of the owner resets it. The
picker of a folder of a home keeps its own count on each frontend (see
[Folders](#folders)).

### List your resources of a kind

```bash
curl -b session=... http://localhost:8080/api/shares/example:note
```

```json
{
  "kind": "example:note",
  "roles": ["use", "manage"],
  "everyone": true,
  "owned": [{"id": "r0123456789abcdef0123456789abcde", "role": "owner", "sharedWith": 2}],
  "shared": [{"id": "r5555555555555555555555555555555", "role": "use", "everyone": true, "owner": "Alice"}]
}
```

`owned` and `shared` list at most 100 resources each; `truncated` says that more
exist. `everyone` on a shared resource says that your role comes from the grant to
`@everyone`.

### Share a resource

```bash
curl -X PUT -b session=... \
  -H 'Content-Type: application/json' \
  -H 'X-LocalAI-Confirm: r0123456789abcdef0123456789abcde' \
  -H 'Sec-Fetch-Site: same-origin' -H 'Sec-Fetch-Mode: cors' -H 'Sec-Fetch-Dest: empty' \
  -d '{"role":"use"}' \
  http://localhost:8080/api/shares/example:note/r0123456789abcdef0123456789abcde/grants/@everyone
```

The answer lists the grants after the change. A grant to `@everyone` has the user
ID `@everyone` and no display name. The call is safe to repeat. If the answer is a
503 with type `outcome_unknown`, repeat the same call.

### Errors

| Status | Type | Meaning |
|--------|------|---------|
| 400 | `invalid_request` | The kind does not grant the role, or the search text is not 3 to 64 characters. |
| 403 | `addon_error` | Not the owner, not a browser session, a person the kind does not accept or whose account is not active, a feature the kind names and the caller does not hold, an unknown kind or an unknown resource. These get the same answer, so a caller cannot tell whether a resource exists. |
| 409 | `sharing_disabled` | The installation runs in local-owner mode, which has no named accounts to share with. |
| 429 | `rate_limited` | The person searched for people too often. Wait a moment and search again. |
| 503 | `outcome_unknown` | A grant change may have run. Repeat the same call. |

## Kinds of LocalAI itself

LocalAI shares three kinds of its own resources with these routes. Their owners get
the resource ID from the page of the resource, or from the route in the table.

| Kind | What it is | Roles | `@everyone` | Feature | Resource ID from |
|------|------------|-------|-------------|---------|------------------|
| `connection` | A [connector binding]({{%relref "/features/chat-apps#sharing-a-connection" %}}) | `use` | No | `connectors` | `POST /api/chats/connectors/{id}/share` |
| `collection` | A [collection]({{%relref "/features/files-and-documents/collections#sharing-a-collection" %}}) | `viewer`, `editor` | Yes | `collections` | `POST /api/collections/{name}/share` |
| `agent` | A [saved agent]({{%relref "/features/chats-and-agents/chats-agents#share-an-agent" %}}) | `use`, `viewer` | Yes | `agents` | `POST /api/chats/agents/{id}/share` |

- **`connection`**: `use` lets the person's chats and agents run the tools of the
  binding's add-on with its credential. A chat of the person with a shared agent
  that names the binding uses it as described in
  [Share an agent]({{%relref "/features/chats-and-agents/chats-agents#share-an-agent" %}}). The person sees the name and the state of
  the binding and never its token or settings; every other operation stays with the
  owner and answers `404` to the person. Messages to the bot still reach the owner's
  agent.
- **`collection`**: `viewer` searches and reads, `editor` also adds and removes
  entries. Sources, reset and sharing stay with the owner. The person finds a shared
  collection by its name; their own collection of that name wins, and the shared one
  is then named `shared:<resource ID>`.

- **`agent`**: `use` chats with the agent as yourself, `viewer` also reads its
  definition. `use` hides the instructions from the UI and the API only: a
  person who runs the agent can ask the model to repeat its instructions, so
  they are not a secret from that person. Only the owner edits, deletes,
  exports or shares it. Sharing the agent shares nothing it uses. The owner
  reads what it uses with `GET /api/chats/agents/{id}/references`: each
  collection, connection and add-on resource carries `share` with the options
  its kind can grant and the role of each option. A person who may use the agent reads
  `GET /api/chats/agents/{id}/needs`: each thing it uses, its network preset and its
  opt-in tool groups, with the state of each for them.

The binding and the collection stay where they are. A share is one record of the
policy that names the binding ID or the collection name of its owner, so nothing is
copied. Work that reads or sends through a shared resource is admitted work of that
record: lowering or revoking cancels it as described above, and the next call gets
the answer of a resource that does not exist. Deleting a binding drops its record
after it revoked every grant.

With several frontends, every frontend lists, shares and uses the bindings and
collections through the policy of the add-on owner (see
[Distributed mode](#distributed-mode)). A search or a write on a shared collection
runs on the frontend that serves the person, under an admission that the owner
holds. The calls through a shared binding always run on the owner, which is the
only process that holds the tokens.

## For service authors

A service shares its resources by implementing `policy.KindProvider`
(`core/services/addons/policy`) and passing it to
`Application.RegisterShareKinds` before the add-on owner starts. The provider
registers a `policy.KindSpec`: the grantable roles, the role table, the rule for who
may receive a grant, whether `@everyone` is allowed, the feature a caller needs
(`Feature`, empty for none), who may create a resource and how to check that the
backing of a pending resource exists. The service then creates
its resources with `Policy.CreateRef` and runs its work under `Policy.AdmitRef`, so
that a lowering or a revocation cancels it. A service that already names its
resources keeps its names: `Policy.EnsureRef` makes the record of a name on its
first share, `Policy.ListingKind` gives the name of each record (`ShareView.Local`)
with its owner, and `Policy.ForgetRef` drops it, after it revoked every grant, when
the resource is deleted. In a process that does not hold the policy, the service
uses `policy.Forwarded` (the same calls, `policy.Sharing`), which asks the owner
through the add-on gateway. Package
`core/services/addons/policy/sharetest` is a complete example that only the tests
use.

## Folders

A folder of a person's home is shared with the same dialog and the same rules
(the owner only, the owner's own browser session, never a tool, `@everyone`
allowed), but not through `/api/shares`. A lowering or a revoke holds from the
grantee's next file call on every frontend, because the file store checks the
grants on every call. The chats of the grantee that run with the folder on the
frontend that serves the change are stopped before the change answers; the other
frontends get the request on the chat bus and stop theirs within seconds. Its
grants are the workspace grants that the file store checks on every file call,
so they stay in the workspace database, and the routes are under
`/api/chats/files/shares` (see [Share a folder of your
home]({{%relref "/features/chats-and-agents/chats#share-a-folder-of-your-home" %}})). Moving them
into the policy store would put the add-on owner on the path of every file
call. The user picker of a folder is the picker described in [API](#api), with the
same rules and limits, but it answers on the frontend that serves the call, so its
count of searches is kept per frontend.

LocalAI reads some folders of a home with the owner's own authority: the skills
(the `.skills` folder at the top of the home and every `.skills` folder below it),
the notes in `Memory/` and the context files of add-ons, the agent profiles and
the persona in `LocalAI/`, and the pages in `pages/`. Two rules keep a grantee
from writing the owner's instructions:

- A person cannot share `LocalAI/`, `Chats/`, `Memory/`, `pages/` or a `.skills`
  folder, or a folder inside one. A folder that holds a grant, on itself or on a
  folder below it, cannot move into another home, out of a home, or into one of
  these folders: the move is refused with type `folder_shared`, also in an undo.
  A grant therefore never reaches another home, where its giver could not take
  it back. An administrator is not held by this rule.
- When LocalAI loads that content, it leaves out every file in a folder on which
  another principal than the owner, `@everyone` included, holds write access
  through the folder or a folder above it. A read grant does not hide anything.

The owner's list of shared folders shows every grant on a folder strictly inside
the home, whoever made it, managed folders included, and the owner can revoke
each one.

## The Share dialog

The web UI has one Share dialog for every resource that can be shared. It shows
who has access and their role, finds people by name, offers `@everyone` where the
kind allows it, asks before it revokes a grant, and says so in local-owner mode.
A search result is only an account ID and a display name: an address is never
shown.

The dialog does not know the routes. It takes a client with four calls (the
Files page passes `folderShareClient(folderSharesApi, id)` for a folder of the
home, with the roles `read` and `write`):

| Call | Answer |
|------|--------|
| `grants()` | `{ grants: [{ userId, displayName, role }] }` |
| `setGrant(principal, role)` | The grants after the change |
| `revoke(principal)` | The grants after the change |
| `searchUsers(query)` | `{ users: [{ id, displayName }] }` |

### In an add-on

An add-on page gets the dialog as `sdk.ui.ShareDialog` and the client of one of
its own resources from `sdk.resources.shareClient(resourceId)`. The client uses
the add-on routes and is bound to the add-on and its installed generation. Add-on
resources have the roles `viewer` and `editor` and no grant to `@everyone`. Render
the dialog in the page while it is open; it draws its own modal frame, so do not
put it inside `sdk.layers.openDialog`.

```jsx
const ShareDialog = sdk.ui.ShareDialog
const client = R.useMemo(() => sdk.resources.shareClient(db.id), [db.id])

{open && ShareDialog ? (
  <ShareDialog
    client={client}
    resource={{ id: db.id, name: db.name }}
    sharing={listing.sharing}
    roles={[
      { id: 'viewer', label: t('role.viewer'), help: t('role.viewerHelp') },
      { id: 'editor', label: t('role.editor'), help: t('role.editorHelp') },
    ]}
    onChanged={refresh}
    onLost={() => { setOpen(false); showUnavailable() }}
    close={() => setOpen(false)}
  />
) : null}
```

The page of a connector add-on shares one of the person's bindings with
`sdk.ui.ConnectionShareDialog`. It takes the binding ID (`binding`), its `name`, the
words of the `use` role (`roleLabel`, `roleHelp`) and the props `intro`, `title`,
`onChanged`, `onLost`, `close` and `testId` of the dialog. The host makes the binding
shareable and calls the sharing routes itself; the page gets no client.

```jsx
const Dialog = sdk.ui.ConnectionShareDialog
{open && Dialog ? <Dialog binding={c.id} name={c.name} close={() => setOpen(false)} /> : null}
```

An add-on's own code cannot reach the sharing routes: `sdk.core.fetch` refuses
`/api/shares`, the `.../share` routes of agents, collections and bindings and the
folder routes under `/api/chats/files/shares`, and drops the `X-LocalAI-Confirm`
header. Only the person's clicks in the host's dialog
share or revoke.

A host that is older than the dialog has no `sdk.ui.ShareDialog`. Check for it and
hide the Share button when it is missing. The SDK version stays 2: the dialog, the
badges and `shareClient` are additions to the SDK 2 surface.

### In a page of the web UI

A page of the React UI imports `ShareDialog` from `components/share/ShareDialog`
and builds the client of a resource of a kind with
`kindShareClient(sharesApi, kind, resourceId)` (`components/share/shareClients.js`
and `utils/api.js`). Take the roles and `everyone` from the answer of
`GET /api/shares/{kind}`; `shareRoles(roles, t)` in `components/share/shareModel.js`
gives the host's words for `use`, `manage`, `viewer` and `editor`. The dialog and
the badges are also in the UI catalog at `/app/ui-catalog`.

### Props

| Prop | Meaning |
|------|---------|
| `client` | The four calls above. |
| `roles` | `[{ id, label, help }]`, the roles the kind grants, in order. The first one is the default role for people you add. `help` is shown under the search field. |
| `allowEveryone` | `true` when the kind may be shared with `@everyone`. |
| `resource` | `{ id, name }`. The name is in the title. |
| `sharing` | `false` in local-owner mode. The dialog then shows that sharing is not available and makes no call. |
| `references` | Optional. Other resources that this one uses, which the owner decides on before sharing: `[{ id, label, kind, options }]`, where `options` is a subset of `view`, `use` and `none`. The dialog does not add anyone until every row has a choice. The agents page takes the options from the server, which offers only the choices the referenced kind can grant (see [Share an agent]({{%relref "/features/chats-and-agents/chats-agents#share-an-agent" %}})). |
| `onShare` | With `references` only: called after each new grant, in the same step, with `{ principal, role, choices }`, where `choices` maps each reference ID to the chosen option. The page shares the referenced resources from there. |

A reference row with no `options` (or only `none`) is shown read-only with the
line "They need access of their own": the dialog asks nothing for it. The agents
page passes such rows for the folders, skills, add-ons, MCP servers and built-in
grants of an agent, and for collections and connections that cannot be shared
in the build.
| `onChanged` | Called after every change. |
| `onLost` | Called when the answer says that the person can no longer share the resource here (403, 401, or the add-on was withdrawn). |
| `close` | Closes the dialog. |
| `title`, `intro` | Replace the default title and the line under it. |

### Role and "Shared by" on a list row

`sdk.ui.RoleBadge` (`role`, `label`) draws the caller's role as a chip.
`sdk.ui.SharedByBadge` (`entry`, `roleLabel`, `hideRole`) draws the role and one
line from a row of a listing: "Shared by Alice", "Shared with everyone by Alice",
"Shared with 2" or "Private". Pass a row of `GET /api/shares/{kind}` as `entry`:
owned rows have `role: "owner"` and `sharedWith`, shared rows have `role`, `owner`
and `everyone`.

## Distributed mode

The grants, and the records of the bindings and collections that LocalAI shares,
are stored only in the policy store of the add-on owner, the single
process that writes them. Frontends hold no copy and forward every call, so no
frontend answers from stale state. The kinds register on the owner when its
policy opens. Each frontend registers its kinds too, but keeps them and does
not use them: the owner decides.

This applies to the `/api/shares` routes and to the services that use a shared
resource. A frontend that serves a person asks the owner, over the add-on
carrier, to list the resources of a kind that are shared with that person, to
create a resource or the record of a binding or a collection, to drop that record
when the binding is deleted, and to admit a piece of work, such as a chat turn with
a shared agent or a search on a shared collection. The work runs on the frontend.
A call through a shared binding is the exception: it runs on the owner, which holds
the tokens, so a credential never reaches a frontend.

### Admitted work on a frontend

The owner holds the admission of work that runs on a frontend under a lease. The
frontend keeps the lease alive with a request that waits on the owner for up to
15 seconds and then starts again.

- When the owner lowers or revokes a grant, the waiting request answers at once.
  The frontend stops the work and keeps the lease alive until the work has
  stopped, then releases it. Only then does the owner's call answer. If that
  takes more than 10 seconds, the answer is the 503 described in
  [Lowering and revoking](#lowering-and-revoking), and the change is in force for
  new work.
- When a frontend stops renewing a lease (it stopped, crashed or lost the
  network), the owner ends the admission 10 seconds after the last request. A
  revocation never waits longer than that for a frontend that is gone.
- When the frontend cannot reach the owner, it stops the work at once. The owner
  could no longer stop it.
- When the owner restarts, it knows no lease. Every frontend stops its admitted
  work at its next request.

One person holds at most 512 leases at once and the owner at most 8192. A person
at their limit gets a denial for the next piece of work until some of it ends; the
work of other people is not affected.

The leases and their counts are the only state of this feature that is not in the
policy store. They are kept per instance, in the owner's memory. The limits of the
records above are counted from the policy store itself, and the startup check runs
on the owner only. Only the owner holds the
policy, so there is no second copy to diverge from. When the owner cannot be
reached, a frontend lists nothing that is shared with the person, creates nothing
and admits nothing. Each call gets the same typed unavailable answer as the
add-on calls, never a grant.
