# Add-on contributed host controls

Date: 2026-11-01

## Goal

The host UI must not know an add-on by name. Today three places do:

- `utils/addonSettings.js` `SERVER_SETTINGS` names `sandbox` (network, bash) and
  `web` (web tools). The add-on's Settings tab shows a read-only summary and links
  to the global Settings page.
- The chat Tools menu Network row and the network cards link to Settings > Tools
  (`SANDBOX_NETWORK_SETTINGS`).
- The global Settings page draws the network of the Sandbox commands in the Tools
  group, even with no add-on that runs commands.

An add-on declares, in its manifest, the host controls it wants on its own page.
The host draws them with its own components and binds them to its own settings.
The add-on's Settings tab then holds the real, editable controls, and the chat's
Network row appears only when an add-on that declares the network control runs
the chat's commands.

## Non-goals

- No new policy. The admin limit, the private switch, the audit, `egress.check`
  and the cards stay as they are and stay in the host.
- No add-on code draws a host control. A control is not a slot: the add-on
  module supplies no component for it.
- No change to the Go side that reads the Sandbox status by its ID to tell host
  isolation from isolated commands (`Isolation`, `Unavailable`). That is a status
  read of one add-on's tool, not UI, and stays for a later change.
- No new endpoint. The controls save with `POST /api/settings`, as before.

## Contract shape

A new optional list in the UI declaration:

```json
"ui": { "controls": ["network-policy", "tool-providers"] }
```

Each entry is the ID of a host component from a closed registry. The list is unique,
at most 8 entries. An unknown identifier refuses the manifest at install, like an
unknown slot. A manifest without the field is valid and unchanged.

| Control | What the host draws | Entitlement (checked by the host) |
|---|---|---|
| `network-policy` | On the add-on's Settings tab: the network of the commands the add-on runs (limit, preset of a new chat, ports, extra registries, always-allowed hosts, private switch with its typed phrase, audit retention, counts, audit). In the chat Tools menu: the Network row and its administrator link. On the network cards: the administrator link. | The add-on holds a host operation grant that covers `egress.check` (the host setting `LOCALAI_ADDON_HOST_OPS`, default `sandbox=egress.`), and it declares a tool that shadows `bash`. |
| `tool-providers` | On the add-on's Settings tab: the rows of Settings > Tools for the built-in tools the add-on replaces (who serves the tool, the choice when several add-ons replace it, the switch of the built-in fallback). | The add-on declares at least one valid tool that shadows a built-in tool. |

The manifest declares; the host decides. The apps list (`GET /api/addons/apps`)
carries `ui.controls` with only the controls the add-on is entitled to now. A
control whose entitlement is missing is dropped from the list, without an error:
an administrator can remove a grant at any time.

### The registry of host components

The IDs name entries of one registry of host-provided components. Each entry has
the same three parts on both sides of the wire:

| Part | Go (`pkg/addons/contract/hostcontrols.go`) | UI (`src/addons/hostControls.js`, components in `hostControlComponents.jsx`) |
|---|---|---|
| ID | `HostControl.ID` | registry key |
| Entitlement | `HostControl.Grant` (a host operation the add-on must be granted) and `HostControl.Shadows` (a built-in tool it must replace; `*` means any) | none: the UI trusts the apps list |
| Placements | none | `settings` (a block on the add-on's Settings tab), `chat.tools` (a row of the chat Tools menu), `chat.card` (the administrator link of a network card) |

A test keeps the IDs of the two registries equal, as it does for the icon names.
A placement component takes only host data (`{ app }` on the Settings tab, the
chat's `egress` view in the Tools menu), never data from the add-on module.

A new host component is a host change with its own review, like a new slot. Using
an existing one is a manifest line plus, when the entry needs one, a grant.

### Worked example: a second add-on

A hypothetical `devbox` add-on runs commands in a remote VM. Its proxy asks the
host with `egress.check`, like the Sandbox. Its manifest:

```json
{
  "id": "devbox",
  "capabilities": {
    "tools": ["bash"],
    "toolDefinitions": [{ "name": "bash", "kind": "action", "shadows": "bash", "inputSchema": { "...": "..." } }],
    "actions": true,
    "ui": { "entry": "ui/devbox.js", "sdk": { "min": 2, "max": 2 }, "controls": ["network-policy", "tool-providers"] }
  }
}
```

The administrator adds the grant: `LOCALAI_ADDON_HOST_OPS=...,devbox=egress.`.
With no host change:

- The Devbox Settings tab shows the same network block and the bash row.
- When an administrator pins Devbox for `bash`, `GET /api/chats/{id}/egress`
  answers `addon: "devbox"`, and the chat Tools menu shows the same Network row,
  whose administrator link opens the Devbox Settings tab.
- Without the grant, the apps list drops `network-policy` for Devbox, the block
  and the row do not appear, and `egress.check` refuses the add-on as before.

The tests install such a second fixture add-on next to the Sandbox (a Go spec of
the entitlement and a Playwright spec of the Settings tab and the chat row).

Why a closed list of strings, and not objects or slots:

- The host owns the component, the settings it binds and where it is drawn, so
  the add-on has nothing to configure. A string is enough, and an object form can
  be added later the way a chat action grew from a bare identifier.
- A slot would hand host settings to add-on code. A control keeps them in host
  code.
- The entitlement reuses the existing grant model (`addon-id=op-prefix`) and the
  existing `shadows` declaration. No new host setting.

### Versioning

The field is additive, like `files.preview` and `skillDefinitions` were. The
manifest schema version and the host SDK version stay the same, so every
installed add-on keeps working. A host older than this change refuses a manifest
with `ui.controls` as an unknown field, as it does for every newer field; the
first-party packages ship with the host that reads them.

## How the host draws and enforces

### Server

- `pkg/addons/contract`: `HostControls` (closed list), validation in
  `validateContributions`, and `EntitledControls(manifest, grants)`.
- `core/services/addons/apps`: `Snapshot.HostOps` carries the grants of the
  add-on; `UIView.Controls` is `EntitledControls(...)`. The manager fills
  `HostOps` from the host setting.
- `GET /api/chats/{id}/egress` gains `addon`: the add-on that runs the chat's
  commands, when the network settings apply (`applies` true) and that add-on's
  entitled controls include `network-policy`. `applies` and `applies_reason` keep their
  meaning: they describe the policy, which the host enforces whatever the UI
  shows.

### UI

- The add-on's Settings tab is offered when the add-on declares a
  `settings.sections` slot or has a control. Below the add-on's own sections it
  draws one host block for each control:
  - `network-policy`: the setting rows of the network fields and the `EgressSection`
    pieces (private switch, registries, audit), in a small draft form with the
    same pending bar, checks and save as the Settings page.
  - `tool-providers`: the `BuiltinToolsSection` rows, filtered to the tools the add-on
    replaces, in the same form.
- `SERVER_SETTINGS`, `serverSettingsFor`, `serverSettingsAddress` and the
  read-only summary are removed.
- The chat Network row is drawn when `applies` and `addon` are set. The
  administrator link goes to that add-on's Settings tab and names the add-on by
  its title from the apps list. The row's DOM, classes and look do not change.
- With no `addon`, the Tools menu shows only the existing line that commands on
  this machine use the server's network (`applies_reason` `host`), or nothing.

### The global Settings page

Rule: the Settings page draws the settings of features that exist without any
add-on, and points to the add-on where an add-on serves them.

- The built-in tool rows (host bash, web search, read web pages) stay. A row
  served by an add-on that has the `tool-providers` control gets a link to that add-on's
  Settings tab.
- The network fields of the commands (`chats_egress_ceiling`, `_default`,
  `_ports`, `_extra_registries`, `_allowed_hosts`, `_allow_private`) belong to the
  `network-policy` control. The Settings page does not draw them, in a group or in search.
  When an add-on has the control, the Tools group shows one row that names the
  add-on and links to its Settings tab.
- `chats_egress_audit_days` also keeps the audit of the commands run on this
  machine, which exists without an add-on. It stays on the Settings page and is
  also drawn in the `network-policy` block.
- `/app/settings?group=tools#settings-egress` (the legacy address) opens the
  Settings tab of the add-on that has the `network-policy` control. With none, it opens
  the Tools group.

## Security

- A control is drawn by host code and saves through `POST /api/settings`, which
  needs an administrator. The add-on supplies no code, no value and no label for
  it, so it cannot change what the control does, hide the warning, or skip the
  typed phrase of the private switch.
- Entitlement is decided on the server from the host setting and the verified
  manifest. An add-on without the `egress.` grant cannot put the network controls
  on its page, so an unrelated add-on cannot present itself as the place that
  sets the network policy. `addon` in the chat view follows the same rule.
- Declaring a control grants nothing. Policy, grants and enforcement do not read
  the declaration: `egress.check` still checks the grant, the limit still caps
  every chat, and the audit is written by the host.
- Add-on UI code is trusted code in the page origin (see the add-on docs). The
  controls do not change that boundary; they only remove host knowledge of names.

## Open questions

1. **An installed Sandbox package older than this change** declares no control.
   Its commands keep the policy, but the chat Network row and the network block
   do not appear until the package is updated. Recommendation: accept it. The
   first-party package ships with the host; the update check offers it. A fallback
   that derives the control from the grant and `shadows` would bring back an
   implicit rule.
2. **Should `tool-providers` also cover replaced skills** (xlsx, docx)? The Add-ons
   details already have a switch for them. Recommendation: not now; add it to the
   same control when an add-on page needs it.
3. **Should the Go status reads stop naming the Sandbox** (`Isolation`,
   `Unavailable`)? Recommendation: yes, in a follow-up, with an `isolation` field
   in the contract of a tool that shadows `bash`.
