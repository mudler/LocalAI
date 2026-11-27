---
name: addons
description: Use when the user asks about LocalAI add-ons - what they are, how an administrator installs one, how to turn one on in a chat - or about the Sandbox add-on that runs commands and code, its profiles and its network, or how plugins differ from add-ons.
---

# Add-ons, the Sandbox and plugins in LocalAI

Full docs: https://localai.io/docs/addons/user-guide/

## What an add-on is

An add-on is a package that gives LocalAI new tools, pages or services: for example a sandbox, a mailbox, office files, a calendar, a chat app such as Telegram. An add-on is code. It runs as the operating system user that runs LocalAI, so install only add-ons you trust.

| Task | Who |
|---|---|
| Install, update, remove an add-on, change the library | An administrator |
| Use an installed add-on in a chat | A user with the `addons` feature |
| Connect their own account (for example a mailbox) | Each user, for their own account |

## Install an add-on (administrator)

1. Open **Add-ons** in the web UI, then **Find add-ons**.
2. Pick an add-on and choose **Install**.
3. Read what the add-on may do on the host. Accept it.
4. Wait for the install queue to finish.
5. Turn the add-on on in **My add-ons**. The install does not turn it on.

An add-on in a zip file:

```bash
local-ai addons inspect my-addon.zip    # check what it holds
local-ai addons install my-addon.zip    # check it and upload it to the server
```

A local file is not signed. Install one only when you know who made it.

Details: https://localai.io/docs/addons/install-and-update/

## Use an add-on in a chat

An installed add-on is not on in every chat.

1. Open the **Tools** menu of the chat.
2. Switch the add-on on.
3. If it talks to a service, pick the connection to use.

A saved agent gets only the add-ons that its settings name.

## The Sandbox

The Sandbox add-on gives a chat a `bash` tool that runs commands and code in an isolated sandbox, with the user's files mounted.

- Turn it on: **Tools** menu of the chat, switch **Sandbox** on.
- Files: the user's files are at `/workspace/home`. Each command starts there.
- Changes: after each command, what changed is saved to the user's files as one change. The user can undo it in the history of Files.
- Limits: a command stops after 120 seconds by default. Output is cut at 64 KiB.
- Isolation: each user and chat has its own sandbox. It is suspended after 10 minutes without use.

### Profiles

| Profile | Has |
|---|---|
| `base` | Python with pandas, matplotlib, openpyxl, python-docx, python-pptx, pypdf, Pillow; pip, jq, ripgrep, sqlite3, zip, git, curl, poppler |
| `media` | `base` plus ffmpeg, sox, ImageMagick |
| `office` | `base` plus LibreOffice Impress, Writer and Calc, and fonts |

Pick the profile in the **Tools** menu. `apt` does not work: you cannot install system programs. Use the profile that has the program.

### Network

The sandbox has no network device. HTTP and HTTPS go through a proxy. The chat has three settings: **Off**, **Custom** (package registries and domains an administrator allowed) and **All public sites**. The default is **Off**, so `pip install` and `curl` fail until the user or an administrator changes it. When a command asks for a host that is not allowed yet, the chat shows a card: **Allow once**, **Allow for this chat** or **Deny**.

### Example: a chart from a CSV

```bash
python3 - <<'PY'
import pandas as pd, matplotlib.pyplot as plt
df = pd.read_csv("sales.csv")
df.plot(x="month", y="total", kind="bar")
plt.savefig("sales.png")
PY
```

The PNG is saved in the user's files.

### When the Sandbox does not work

| You see | Do this |
|---|---|
| The chat says it cannot run commands | The Sandbox page names the backend that runs, or why none works. The host needs KVM or gVisor. |
| `pip install` fails | The network is Off. Ask for **Custom** or ask an administrator to raise the limit. |
| A program is missing | Switch to the profile that has it. |

Details: https://localai.io/docs/addons/catalogue/work/sandbox/ and https://localai.io/docs/addons/catalogue/work/sandbox-settings/

## Plugins and add-ons

| | Add-on | Plugin |
|---|---|---|
| What it is | A package of code that runs on the host | A folder of skills, commands, sub-agents and MCP servers, in the Claude Code plugin format |
| Comes from | An add-on library | A marketplace: a git repository or a `marketplace.json` |
| Who installs | An administrator | A user, for themselves, when the plugin runs no code; an administrator otherwise |

A plugin that runs code (a hook, a stdio MCP server, a shell command in a skill or a command) needs an administrator, who installs it for everyone. LocalAI never runs plugin code on the host. Some kinds of plugin code are not supported yet; the install review lists the components that LocalAI skips.

```bash
local-ai plugins browse python               # search the marketplaces
local-ai plugins install localai@localai     # install a plugin
local-ai plugins list                        # what is installed, and on for you
local-ai plugins enable localai              # turn it on for you
```

In a chat, plugin skills are named `<plugin>:<skill>` and commands are typed `/<plugin>:<command> <arguments>`. The installed files are read only, at `/workspace/shared-plugins/<digest>/`.

Details: https://localai.io/docs/features/plugins/
