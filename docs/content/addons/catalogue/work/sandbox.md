+++
disableToc = false
title = "Sandbox: run Python on your files"
description = "An add-on that runs commands and code for a chat in an isolated runtime."
weight = 20
aliases = ["/features/sandbox/"]
+++

Sandbox is an add-on. It lets a chat run commands and code, for example to make a chart, convert a file or run a script. The commands run in an isolated sandbox with your files mounted. The Sandbox add-on is for Linux. In the current development builds, it is in the add-on library in the development channel, so it can change.

## What you need

- An administrator who installs the add-on. See the [add-on user guide]({{% relref "/addons/user-guide" %}}).
- A host that can run an isolated backend. The add-on tries KVM (Cloud Hypervisor) first, then gVisor. KVM needs `/dev/kvm`. gVisor needs `runsc` and permission to create a user namespace. If neither works, no command runs, and the chat says so.
- The chats and their files must be on, because the sandbox works on your files.
- A user who has the `addons` feature, on a server with accounts.

## Turn it on for a chat

1. Open the **Tools** menu of the chat.
2. Switch **Sandbox** on.

When it is on, the `bash` tool of the chat runs in the sandbox. Without it, a chat can run commands on the server only if an administrator allows it, and then there is no isolation. See [Running commands]({{% relref "/features/chats-and-agents/chats#running-commands" %}}).

## How it works

- Each user and chat has its own sandbox. The model cannot name another user or chat.
- Your files are mounted under `/workspace/home`. Each command starts there.
- After each command, what changed is saved to your files as one change. You can undo it in the history of Files. If someone else changed the same file, their version stays and the sandbox version is saved as a copy.
- A sandbox is suspended after 10 minutes without use and removed after 24 hours.
- An answer is cut at 64 KiB. A command stops after 120 seconds by default, and after 28 minutes at most.

## Network

A sandbox has no network device. HTTP and HTTPS go through a proxy that asks LocalAI for each connection. The **Network** row in the Tools menu has three choices: **Off**, **Package registries** and **All public sites**. The limit that an administrator sets is a ceiling. The default limit is Off, so `pip install` does not work until an administrator allows it.

When a command asks for a host that the chat does not allow but the limit allows, the chat shows a card. Choose **Allow once**, **Allow for this chat** or **Deny**. See [Network of the Sandbox commands]({{% relref "/features/chats-and-agents/chats#network-of-the-sandbox-commands" %}}).

## What is installed

The `base` profile has Python with pandas, matplotlib, openpyxl, python-docx, python-pptx, pypdf and Pillow, and tools such as jq, ripgrep, sqlite3, zip, git and curl. The `media` profile adds ffmpeg, sox and ImageMagick. The `video` profile adds a Rust toolchain and a video renderer. You pick the profile in the Tools menu, unless an administrator limits the choice. Programs cannot be installed with `apt`. See [Sandbox profiles]({{% relref "/addons/contract-reference#sandbox-profiles" %}}).

Example: ask "Make a bar chart of the numbers in sales.csv and save it as a PNG". The model reads the file, runs a Python script, and saves the image in your files.

## Troubleshooting

| You see | Do this |
|---|---|
| The chat says it cannot run commands | Open the Sandbox page. It names the backend that runs, or why none works. An administrator sees the reason for each backend. |
| gVisor fails with `cannot read client sync file` | The host needs an AppArmor profile that allows user namespaces, and a container that may create them. |
| `pip install` fails | The network is Off. Ask an administrator to raise the limit. |

For the settings and the host backend, see [Sandbox backends and settings]({{% relref "/addons/contract-reference#sandbox-backends-and-settings" %}}).
