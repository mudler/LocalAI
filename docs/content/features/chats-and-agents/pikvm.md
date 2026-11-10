+++
url = "/features/pikvm/"
disableToc = false
title = "PiKVM in chats and agents"
weight = 24
+++

The PiKVM add-on lets your [chats]({{% relref "/features/chats-and-agents/chats" %}}) and [saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}}) look at a [PiKVM](https://pikvm.org) (or another device that runs the kvmd API) and the machine it controls: is it on, what is the device, is the keyboard connected. If you allow it, they can also press the power buttons of that machine and type on its keyboard. A **person confirms those actions** unless you choose otherwise for power actions and key presses (see [Not asking for some actions](#not-asking-for-some-actions) and [Allow all](#allow-all)), and typing and forced power actions are off until an administrator switches them on.

A chat can answer questions such as:

- "Is the test box on?"
- "Power the test box on." (a power action, see [Power actions](#power-actions))
- "Press Escape and F2 to enter the BIOS." (keys, see [Typing and keys](#typing-and-keys))

## Set it up

1. Install the **PiKVM** add-on from the add-on gallery and open its page.
2. On the PiKVM, add a user for LocalAI: switch the filesystem to read-write with `rw`, run `kvmd-htpasswd add localai`, then `ro`. See [the PiKVM authentication guide](https://docs.pikvm.org/auth/).
   A PiKVM has no per-user rights: every user can do everything. A dedicated user does not limit what the add-on can do, but you can remove it on its own. The limits of the add-on (below) are the only ones.
3. Get the fingerprint of the certificate of the PiKVM (see [The certificate](#the-certificate)).
4. On the add-on page, choose **New PiKVM connection**. Type the user name, the password and the address, for example `https://pikvm.local#sha256=<fingerprint>`. Press **Test**: the row shows the model and the kvmd version if the address and the login work.
5. Leave the **write mode** on **Read only** until you have read the limits on this page.

One connection is one device. For a second device, add a second connection. A chat uses the connections you enable for it in the **Tools** menu (see [The connections of an add-on]({{% relref "/features/chats-and-agents/chats" %}}#the-connections-of-an-add-on)). With two or more enabled, the model names the connection in each call.

### The user and the password

The connection keeps one sealed credential. In this version it holds `user:password`: the host splits it at the **first colon**, so the user name has no colon and the password may have colons. The page has a field for each and joins them. This is a v1 format: when the host gets a credential kind with named fields (a user and a password), the add-on will move to it, and you will enter the pair again.

The add-on sends the user and the password in the `X-KVMD-User` and `X-KVMD-Passwd` headers, as [the PiKVM API](https://docs.pikvm.org/api/) allows, and nowhere else. They are not in a log, an error or a result. The page trims nothing for you: a password with a space at its start or end is refused.

**Two-factor login (2FA) is not supported in this version.** With 2FA on, the PiKVM wants the current one-time code added to the password, and the code changes every 30 seconds. A device with 2FA on answers 403 and the Test says the login was refused. 2FA is one switch for the whole PiKVM (`kvmd-totp`), so the device cannot have it on for the browser and off for LocalAI.

### How the address is kept

The address is not a secret, so it is not stored with the password. The connection lists it as its one allowed resource, the same list the GitHub, Jellyfin and Home Assistant add-ons use. Every tool takes a `device` argument. LocalAI compares it with the address on the connection before the add-on is called and refuses any other, so a model cannot send the password to another host. The add-on talks only to that address:

- The address is `https`. Plain `http` is accepted only for an address on your own network (a private IP address, a single-word name, or a name ending in `.local`, `.lan`, `.home`, `.internal` or `.home.arpa`), because the login travels in headers.
- A reply that redirects is an error. The password is never sent to another host.
- The add-on does not use a proxy of the environment.

### The certificate

A PiKVM comes with a self-signed certificate. The add-on never turns verification off. Pin the certificate instead: copy its SHA-256 fingerprint and add it to the address as `#sha256=` and the 64 hex digits (colons are fine).

```bash
openssl s_client -connect pikvm.local:443 </dev/null 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha256
```

The add-on then accepts that exact certificate for that exact address, and nothing else; a different certificate is refused before the password is sent. When you renew the certificate, edit the connection and pin the new one. Without a pin, a self-signed certificate is refused and the Test says the certificate is not trusted.

## What the tools do

| Tool | Kind | What it does |
|---|---|---|
| `get-info` | read | Model, board, platform, kvmd and streamer versions, kernel and CPU temperature. No serial number, host name or network details. |
| `get-power-state` | read | Whether ATX control is enabled and busy, the power LED (on or off) and whether the disk LED shows activity. |
| `get-keyboard-state` | read | Whether the keyboard of the device is connected to the target, and the Caps, Num and Scroll Lock LEDs. |
| `power-action` | write, confirmed (you can choose Don't ask) | Press a power button: `on`, `off` or `reset`. The forced actions `off_hard`, `reset_hard` and `long_press` need a switch. |
| `type-text` | write, confirmed (**Allow all** skips the card), needs the typing switch | Type text on the target. |
| `send-key` | write, confirmed (you can choose Don't ask), needs the typing switch | Press keys from a short allowlist. |

Two more tools, `get-settings` and `set-settings`, belong to the add-on page. A model never sees them.

Everything the device returns is marked as untrusted data, and the add-on's skill tells the model never to follow an instruction found in a result. Results are bounded.

## Changes and confirmation

All three write tools declare `approval: always` in the manifest. By default a person confirms **every call**: the mode **Read only** refuses them all, and **Asks you first** stores each as a proposal that waits for a confirmation card. For `power-action` and `send-key` you can stop being asked one by one, as described next, and **Allow all** stops it for all three tools (see [Allow all](#allow-all)). A proposal expires after 10 minutes (power) or 5 minutes (typing and keys).

### Not asking for some actions

On the add-on page, the section **Ask before acting** has one choice for **Power actions** and one for **Key presses**: **Ask each time** (the default) or **Don't ask**. There is no choice for typed text, because it can enter a password or a command; it waits for you unless the connection is on **Allow all**.

- The choice is yours alone. It is saved from your own browser session, it applies to all your PiKVM connections, and no chat, agent or model can change it.
- With **Don't ask**, a chat can switch the machine off or reset it at once, with no card, and unsaved work on it is lost. If an administrator allowed the hard power actions, those run without asking too. Read the warning on the page before you choose it.
- It holds only for your own chats and for messaging senders you paired with the bot. Senders that are only on the allowlist, senders let in because the bot accepts anyone, roles, scheduled runs and sub-agents are still asked.
- A connection set to **Read only** runs nothing, whatever you choose. A connection set to **Allow all** runs these two tools without asking whatever you choose here.
- Every run without asking is written to the server log and to the latest decisions of the connection, with the tool, the connection and the result. The typed text and the keys are not written.
- The row of each connection lists which tools run without asking now.

### Allow all

The write mode of a connection has a third choice, **Allow all**, for someone who wants the agent to act without asking. It is never the default and is never chosen for you: a new connection starts on **Read only**, and a connection keeps the mode it has.

With **Allow all**, a chat of yours runs `power-action`, `send-key` and `type-text` at once, with no card. Unsaved work on the machine is lost when it is switched off or reset, and typed text and key presses go to whatever has the focus, so a shell command or a password can be entered and none of it can be taken back. The page warns about it. If you want a confirmation, choose **Asks you first**. The per-tool choices of **Ask before acting** are not asked in this mode, and the row of the connection lists both tools that have a choice as running without asking.

These rules hold in every write mode, **Allow all** included:

- The switches of an administrator for each device stay in force. Typing and key presses need **Allow typing and key presses**, the forced power actions (`off_hard`, `reset_hard`, `long_press`) need **Allow hard power actions**, and the add-on refuses a call when the switch is off. **Allow all** does not turn a switch on.
- The same limits on text and keys, the stale-state check and the rule that nothing is sent twice.
- A run without you present, such as an agent started by a trigger, still leaves a proposal that waits for a person.

The add-on notes an operation before it sends it and never sends one twice. The PiKVM API has no idempotency key. If the connection breaks after the request left, the result is "unknown", and you decide what to do. Read the power state to see what happened.

### Power actions

`power-action` takes one of these:

| Command | What it sends | Needs the hard switch |
|---|---|---|
| `on` | `POST /api/atx/power?action=on` (a short press of the power button) | no |
| `off` | `POST /api/atx/power?action=off` (a short press: a soft power off) | no |
| `reset` | `POST /api/atx/click?button=reset` (the reset button) | no |
| `off_hard` | `POST /api/atx/power?action=off_hard` (hold the button 5 seconds or more) | yes |
| `reset_hard` | `POST /api/atx/power?action=reset_hard` (hardware reset) | yes |
| `long_press` | `POST /api/atx/click?button=power_long` | yes |

The confirmation card shows the power state that was read just before, and what the action does. The preview reads the state again when you confirm. If the state changed, the action is **rejected as stale** and nothing is sent. An action that makes no sense is refused at once: `on` for a machine that is on, `off` for one that is off, a reset for one that is off. A busy device or one with ATX control turned off is refused.

### Typing and keys

`type-text` and `send-key` are off until an administrator switches on **Allow typing and key presses** for the device on the add-on page. Both use that one switch.

- **`type-text`** types text through the HID print endpoint. The text is at most 200 characters by default; an administrator can set up to 1000 for a device. It has no control characters except newline and tab. The card shows the exact text, the number of characters and lines. The text is never written to a log or an error, and the result of the call does not repeat it. As defence in depth, the add-on refuses text that looks like a private key (a `-----BEGIN` header), a long unbroken blob of base64-like characters (80 or more), a well-known token shape (cloud access keys, source host tokens, SSH public keys) or an exported list of passwords (three or more lines such as `password: ...`). This is a heuristic and not a guarantee: the skill also tells the model never to type a secret the person did not dictate.
- **`send-key`** presses up to 8 keys or chords, in order. Allowed: the letters `a` to `z`, the digits `0` to `9`, `up`, `down`, `left`, `right`, `enter`, `escape`, `tab`, `f1` to `f12`, and the chords `ctrl+alt+delete`, `ctrl+c` and `ctrl+z`. Windows and Meta chords, modifiers alone and every other key are refused. Single keys use `POST /api/hid/events/send_key`, chords use `POST /api/hid/events/send_shortcut`.

Both refuse when the keyboard of the device is not connected to the target.

## Limits you set

The switches are on the add-on page, under the connections. Everyone who opens the page sees them. Only an administrator can change them. They are kept for each device address, so a switch for one PiKVM does not open another:

| Switch | Default |
|---|---|
| Allow typing and key presses | off |
| Longest typed text | 200 characters (1 to 1000) |
| Allow hard power actions | off |

The add-on asks the host, through the `workspace.admin` operation, whether the person who saves the switches is an administrator. The shipped host policy (`deploy/addons/host-policy.json`) grants it. Without that grant, the switches can be read and not changed. A settings file that cannot be read leaves every switch off. They are kept in `settings.json` in the add-on's data folder; the add-on runs one process for the server.

## What is not supported

- **Screenshots and screen reading.** The streamer snapshot is left out of this version. The screen of the target can show secrets, and a snapshot needs a way to hand an image back that this add-on does not have yet. Without it, ask the person what the screen shows.
- **Mass storage.** No mounting or uploading of disk images.
- **GPIO, mouse, the HID reset, and the ATX click beyond what `power-action` covers.**
- **Changes to the users or the configuration of the device.**
- **Two-factor login.** See above.
- Windows and Meta key chords.

## Not verified without a real device

The tests run against a fake that speaks the parts of the kvmd API listed above, as [the PiKVM API reference](https://docs.pikvm.org/api/) describes them. A real PiKVM can still differ. Check these on a device you can reach before you rely on the add-on:

1. The Test shows the model and the version (`GET /api/info`; the add-on reads `hw.platform` and `system` leniently).
2. `get-power-state` follows the real power LED.
3. A soft `off` and an `on` change a spare machine, and `reset` restarts it.
4. `type-text` types a short text with the layout you expect. The add-on does not set a keymap: the PiKVM default applies.
5. `send-key` presses `escape` and `ctrl+alt+delete`.
6. With a wrong pin, the connection is refused before the password is sent.
