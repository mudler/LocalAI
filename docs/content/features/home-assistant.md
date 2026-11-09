+++
disableToc = false
title = "Home Assistant in chats and agents"
weight = 23
+++

The Home Assistant add-on lets your [chats]({{% relref "/features/chats" %}}) and [saved agents]({{% relref "/features/chats-agents" %}}) read your smart home: find entities, read a state, look at a history, list what is unavailable. If you allow it, they can also switch things, such as a light or a thermostat. You set the limits. The add-on never operates a lock, an alarm, a garage door or a siren, whatever you choose.

A chat can answer questions such as:

- "Is the office warmer than the bedroom, and was it warmer yesterday?"
- "Which devices are unavailable?"
- "Turn off the lights in the living room." (a change, see [Changes](#changes-and-confirmation))

## Set it up

1. Install the **Home Assistant** add-on from the add-on gallery and open its page.
2. In Home Assistant, open your profile (your name at the bottom of the sidebar), then the **Security** tab. Under **Long-lived access tokens**, choose **Create token**, name it (for example `LocalAI`) and copy it. Home Assistant shows it once.
   The token has the rights of the user that made it. If you can, make it with a user that sees only what you want to share.
3. On the add-on page, choose **New Home Assistant connection**. Paste the token and write the address you use to open Home Assistant in a browser, for example `https://homeassistant.local:8123`. Press **Test**: the row shows the Home Assistant version if the address and the token work.
4. Leave the **write mode** on **Read only** until you have read the limits on this page.

One connection is one home: one token and one address. For a second home, add a second connection. A chat uses the connections you enable for it in the **Tools** menu (see [The connections of an add-on]({{% relref "/features/chats" %}}#the-connections-of-an-add-on)). With two or more enabled, the model names the connection in each call.

### How the address is kept

The address is not a secret, so it is not stored with the token. The connection lists it as its one allowed resource, the same list the GitHub add-on uses for repositories. Every tool takes an `instance` argument. LocalAI compares it with the address on the connection before the add-on is called and refuses any other, so a model cannot send the token to another host. The add-on talks only to that address:

- The address is `https`. Plain `http` is accepted only for an address on your own network (a private IP address, a single-word name, or a name ending in `.local`, `.lan`, `.home`, `.internal` or `.home.arpa`).
- A reply that redirects is an error. The token is never sent to another host.
- The token goes in the `Authorization` header and nowhere else. It is not in a log, an error or a result.

### A self-signed certificate

The add-on checks the certificate of Home Assistant. If yours is self-signed, the Test says the certificate is not trusted. Do not turn the check off. Pin the certificate instead: copy its SHA-256 fingerprint and add it to **Pinned certificates** in the limits below, as `host:port=fingerprint`.

```bash
openssl s_client -connect homeassistant.local:8123 </dev/null 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha256
```

The add-on then accepts that exact certificate for that exact `host:port`, and nothing else. When the certificate is renewed, pin the new one.

## What the tools do

| Tool | Kind | What it does |
|---|---|---|
| `get-config` | read | Version, time zone, units, language and state of the server. No address, path or location. |
| `list-areas` | read | The areas with entity and device counts, the visible entities by domain and how many are unavailable. |
| `search-entities` | read | Entities by words, domain, area or state, at most 50 per call, with the total. |
| `get-state` | read | One entity with its attributes. |
| `get-history` | read | The state changes of one entity over up to 168 hours, at most 100 points, with min, max and mean for a number. |
| `get-logbook` | read | The newest 50 logbook entries of one entity over up to 168 hours. |
| `list-services` | read | The services the policy lets a model call, with their fields and the write tool that calls each. |
| `call-service` | write | A quiet service: lights, switches, input booleans, media transport. |
| `call-physical-service` | write, always confirmed | Any other allowed service: heating, blinds, scenes, scripts, notifications. |

Two more tools, `get-policy` and `set-policy`, belong to the add-on page. A model never sees them.

There is no tool that renders a template, fires an event or sets a state, because each can run arbitrary logic on your server.

Everything Home Assistant returns (names, states, attributes, logbook text) was written by devices, integrations and people. Every result is marked as untrusted data, and the add-on's skill tells the model never to follow an instruction found in one. Results are bounded: a list is cut and says so, text is cut, an attribute value is cut, and a result above the size limit is refused.

The add-on removes these from every result: `entity_picture`, `access_token`, anything with `token`, `password` or `secret` in its name, a value that carries a token in an address, and the location of people and devices (`latitude`, `longitude`, `gps_accuracy` and similar). It never returns a camera snapshot address.

## Limits you set

The limits are on the add-on page, under the connections. Everyone who opens the page sees them. Only an administrator can change them. They apply to every Home Assistant connection of the server.

### Services a model may call

The allowlist holds `domain.service` entries, or `domain.*` for the services of a domain. A service that is not on it is refused. The default is small:

| Domain | Services | Class |
|---|---|---|
| `light` | `turn_on`, `turn_off`, `toggle` | quiet |
| `switch` | `turn_on`, `turn_off`, `toggle` | quiet |
| `input_boolean` | `*` (except reload) | quiet |
| `media_player` | `media_play`, `media_pause`, `media_play_pause`, `media_stop`, `media_next_track`, `media_previous_track`, `volume_up`, `volume_down`, `volume_set`, `volume_mute` | quiet |
| `climate` | `set_temperature`, `set_hvac_mode` | confirmed every time |
| `scene` | `turn_on` | confirmed every time |
| `script` | `turn_on` | confirmed every time |

`notify` services are not on the list. Add one only if you want a model to send messages.

A script can do anything you wrote into it, and a switch can power a heater. Allow only what you would let a guest in your house do.

### Never available

These cannot be allowed. The add-on refuses them when you save the limits, and again when a call is made, so a hand-edited file does not change it:

| Refused | Why |
|---|---|
| `lock.*`, `alarm_control_panel.*` | They open doors and disarm alarms. A mistaken or injected request must not do that. |
| `siren.*`, `valve.*`, `water_heater.*` | A noise, a flood or a burn. |
| `camera.*` | It writes files and records. |
| `cover.*` on a garage door, a gate or a door | A cover that reports any other device class can be moved once you allow `cover` services. A cover with no device class is treated as a garage door and is never moved. |
| `homeassistant.*` | It restarts or stops the server, reloads the configuration, and its `turn_on` and `turn_off` act on any entity, which would skip the domain checks. |
| Any `reload` or `reload_*` service, `restart`, `stop`, `check_config`, `set_location`, `install`, `create`, `delete`, `remove` | They change the configuration or the system. |
| `hassio.*`, `shell_command.*`, `python_script.*`, `command_line.*`, `rest_command.*`, `mqtt.*` | They run programs or reach outside the home. |
| `recorder.*`, `logger.*`, `system_log.*`, `backup.*`, `update.*` | They erase history, change logging, restore a backup or install an update. |

You can still let a chat read what a lock reports (see below). Reading is never operating.

### Entities the tools never show or touch

Excluded entities are left out of every search, state, history, logbook and area count, and no service call can name them. You can exclude by:

- **Domain.** Default: `lock`, `alarm_control_panel` and `camera`. Remove one to let a chat read it. A lock you read is still never operated.
- **Area.** The entities of the areas you list. If Home Assistant cannot answer which entities an area holds, the tools refuse instead of guessing.
- **Pattern.** A regular expression matched against the whole entity ID, such as `sensor\..*_bedroom`.
- **Label.** Entities that carry the Home Assistant label you name (default `sensitive`). An older Home Assistant that does not know labels cannot check it. The result then says that the label exclusion was not applied.

### How many things one call switches

A call names its entities one by one. The default is 5, the highest you can set is 20. Area, device, label and `all` targets are not accepted: the model has to name each entity, so you see each one in the confirmation.

## Changes and confirmation

A connection has a **write mode**, which the host enforces before the add-on is called:

| Mode | What happens |
|---|---|
| Read only (default) | Both write tools are refused. |
| Asks you first | Each change is stored as a proposal. You see what will be switched, with each entity's name and current state, and you confirm or cancel it. |
| Acts without asking | A chat of yours makes a quiet change at once. Runs without you present still wait for your confirmation. |

`call-physical-service` is always confirmed, even in the last mode. A proposal for it expires after 15 minutes, a quiet one after two hours. Changing the limits after a preview makes an old proposal stale: it is rejected and nothing is sent.

Home Assistant has no idempotency key. The add-on notes an operation before it sends it and never sends one twice. If the connection breaks after the call left, the result is "unknown", and you decide what to do.

## Install notes

- The add-on asks the host, through the `workspace.admin` operation, whether the person who saves the limits is an administrator. The shipped host policy (`deploy/addons/host-policy.json`) grants it. Without that grant, the limits can be read and not changed.
- The limits are kept in `policy.json` in the add-on's data folder. A file that cannot be read, or that names a service that is never available, is refused as a whole: the tools stop working until an administrator saves the limits again. They do not fall back to something wider.
- The add-on runs one process for the server, so every frontend of a cluster reads the same limits.
- It is a Linux and macOS add-on, like the other tool add-ons.

## Not verified without a real instance

The tests run against a fake Home Assistant that speaks the REST API. The fake answers the fixed templates of the add-on (areas and labels) by their shape, so a real instance can still differ: area and label questions use `POST /api/template` with fixed templates and no text from a model. If an area question fails on your Home Assistant, `list-areas` says so and the area exclusion refuses to guess.
