+++
disableToc = false
title = "Sandbox: run Python on your files"
description = "An add-on that runs commands and code for a chat in an isolated runtime."
weight = 20
aliases = ["/features/sandbox/"]
+++

## At a glance

| Topic | Detail |
|---|---|
| What it adds | A `bash` tool that runs commands and code in an isolated sandbox with your files mounted. It makes charts, converts files and runs scripts. |
| Needs | A Linux host that can run KVM or gVisor, or a Mac with Apple silicon, and the chats and their files turned on. |

Ask a chat to make a bar chart from `sales.csv`. It runs the code in an isolated sandbox with your files mounted and saves the PNG in your files. Sandbox can also convert a file or run a script. It runs on Linux, and on macOS with Apple silicon.

## What you need

- An administrator who installs the add-on. See the [user guide]({{% relref "/addons/user-guide" %}}).
- A host that can run an isolated backend. The add-on tries KVM (Cloud Hypervisor) first, then gVisor. KVM needs `/dev/kvm`. gVisor needs `runsc` and permission to create a user namespace. If neither works, no command runs, and the chat says so.
- The chats and their files turned on, because the sandbox works on your files.

## Turn it on for a chat

1. Open the **Tools** menu of the chat.
2. Switch **Sandbox** on.

When it is on, the `bash` tool of the chat runs in the sandbox. Without it, a chat can run commands on the server only if an administrator allows it, and then nothing isolates them. See [Running commands]({{% relref "/features/chats-and-agents/chats-commands#running-commands" %}}).

## How it works

- Each user and chat has its own sandbox. The model cannot name another user or chat.
- Your files are mounted under `/workspace/home`. Each command starts there.
- After each command, what changed is saved to your files as one change. You can undo it in the history of Files. If someone else changed the same file, their version stays and the sandbox version is saved as a copy.
- A sandbox is suspended after 10 minutes without use.
- An answer is cut at 64 KiB. A command stops after 120 seconds by default, and after 28 minutes at most.

## Network

A sandbox has no network device. HTTP and HTTPS go through a proxy that asks LocalAI for each connection. The **Network** row in the Tools menu has three choices: **Off**, **Custom** (the package registries and the domains an administrator added) and **All public sites**. An administrator sets the most a chat can choose. The default is Off, so `pip install` fails until an administrator raises it.

When a command asks for a host that the chat does not allow but the limit allows, the chat shows a card. Choose **Allow once**, **Allow for this chat** or **Deny**. See [Network of the Sandbox commands]({{% relref "/features/chats-and-agents/chats-commands#network-of-the-sandbox-commands" %}}).

## What is installed

The `base` profile has Python with pandas, matplotlib, openpyxl, python-docx, python-pptx, pypdf and Pillow, and tools such as jq, ripgrep, sqlite3, zip, git and curl. The `media` profile adds ffmpeg, sox and ImageMagick. The opt-in `office` profile adds LibreOffice Impress, Writer and Calc, so a presentation or a document can be rendered to images and a workbook recalculated. With the `media` profile, the built-in `assemble-media` skill can join, trim and subtitle clips and build short videos with ffmpeg (a title card, a slideshow) as MP4. Look at the result before you use it. You pick the profile in the Tools menu, unless an administrator limits the choice. Programs cannot be installed with `apt`. See [Sandbox profiles]({{% relref "/addons/catalogue/work/sandbox-settings#sandbox-profiles" %}}).

## Troubleshooting

| You see | Do this |
|---|---|
| The chat says it cannot run commands | Open the Sandbox page. It names the backend that runs, or why none works. An administrator sees the reason for each backend. |
| gVisor fails with `cannot read client sync file` | The host needs an AppArmor profile that allows user namespaces, and a container that may create them. |
| `pip install` fails | The network is Off. Ask an administrator to raise the most a chat can choose. |

For the settings and the host backend, see [Sandbox backends and settings]({{% relref "/addons/catalogue/work/sandbox-settings#sandbox-backends-and-settings" %}}).
