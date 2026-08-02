+++
disableToc = false
title = "Terminal agent"
weight = 24
url = '/features/terminal-agent'
+++

{{< agentic-routing current="terminal-agent" >}}

`local-ai chat` opens a terminal agent that runs against your LocalAI server. It is not a
separate tool: the agent is compiled into the `local-ai` binary, so if you have LocalAI you
already have it.

It works in the shell you already have open. It reads your files, runs commands on your
machine, delegates to sub-agents, and loads MCP servers, plugins and skills. Read-only work
happens on its own; anything that can change something asks you first.

The agent is [nib](https://github.com/mudler/nib) embedded in LocalAI, which is why its
plugin, skill and MCP ecosystem works here unchanged.

{{% notice warning %}}
**Breaking change.** `local-ai chat` used to be a plain chat prompt. It is now an agent that
executes shell commands on the machine it runs on, behind an approval gate you answer. The
old `/clear` command is gone, and `/compact` is the nearest equivalent.
{{% /notice %}}

## Start a session

```bash
# Terminal 1
local-ai run

# Terminal 2
local-ai chat
```

That opens the full-screen interface. Pass `--cli` for a plain, pipe-friendly session
instead.

If nothing is listening, an interactive session offers to start a server for you and stops it
again when you exit. Anywhere else, for instance in a script, it fails and points you at
`local-ai run`.

### How the model is chosen

The agent picks the model in this order, and stops at the first one that answers:

1. `--model`, if you passed it.
2. The model saved in the agent's config.
3. The only model the server offers, when there is exactly one.
4. An interactive picker, when there are several and you are at a terminal. Your choice is
   saved, so the question is asked once.
5. Otherwise it fails, listing the models the server does offer.

## Summon it with Ctrl+Space

`local-ai chat --init` prints a shell widget that binds `Ctrl+Space` to the agent, so you can
call it from a half-typed command line without losing what you were doing:

```bash
# zsh
echo 'eval "$(local-ai chat --init zsh)"' >> ~/.zshrc
# bash
echo 'eval "$(local-ai chat --init bash)"' >> ~/.bashrc
# fish
echo 'local-ai chat --init fish | source' >> ~/.config/fish/config.fish
```

## Inside a session

- `/models` lists the models the server offers, marking the one you are using.
- `/model <name>` switches model and keeps the conversation you are in.
- `/compact` summarises the history so far to free up context.

## Tool approval

Read-only tools run without asking. That covers reads and searches, and read-only shell
commands such as `ls` and `cat`, so the agent can look around and answer you without
interrupting. Everything else prompts for approval, and you can approve, deny, or trust the
tool for the rest of the session.

nib's README keeps the
[exact list of what counts as read-only](https://github.com/mudler/nib#what-a-piped-run-may-and-may-not-do),
along with the [approval modes](https://github.com/mudler/nib#tool-approval) you can set in
the agent's own config.

`--yolo`, or `LOCALAI_CHAT_YOLO=1`, approves every tool call without asking. It is the right
setting for a sandbox and the wrong one for your laptop.

## Piping and scripting

`--cli` makes the agent answer a piped question and exit:

```bash
echo "what is 2+2" | local-ai chat --cli
```

That exits `0`. Read-only tools still run, so a piped question can inspect a repository or a
log file to answer you.

A tool call that is not read-only is a different matter: there is nobody at the keyboard to
approve it, so the call is denied and the session exits `3`. The code is deliberately not `1`,
so a script can tell "I refused to act" apart from "I crashed".

Pipe a question in without `--cli` and the agent refuses, with a message naming `--cli`. It
will not render a full-screen interface into a pipe that cannot show it.

Redirecting the output is not the same thing and is not refused: `local-ai chat > out.txt`
from a terminal draws the interface on `/dev/tty` and writes only the command you pick with
`Ctrl+Y` to the file. That is the same capture the `Ctrl+Space` widget is built on, so both
work. It is the stdin that has to be a terminal.

## Where the agent keeps its state

Config, plugins and skills live in `~/.config/localai/chat/`, or under `$XDG_CONFIG_HOME`
when you have set it. Override it with `--config-dir`, or with
`LOCALAI_CHAT_CONFIG_DIR`. The directory is user-scoped rather than server-scoped, because
the agent is a client and may be pointed at a remote LocalAI.

## Plugins, skills and MCP servers

The agent's own management commands are reached by passing them through:

```bash
local-ai chat plugin install https://github.com/user/plugin
local-ai chat skill list
local-ai chat mcp add my-server -- npx -y @modelcontextprotocol/server-filesystem /tmp
```

Everything after the first positional argument is forwarded verbatim, so LocalAI's own flags
have to come first:

```bash
local-ai chat --config-dir /srv/agent plugin list
```

Claude Code plugins install as they are, so a plugin written for that format needs no
conversion.

{{% notice warning %}}
**Pass `--yes` in scripts.** The management commands do not read the streams LocalAI hands
them, so `plugin install` without `--yes` in a non-interactive context installs the plugin
and leaves it **disabled**. It prints that it did, on the line
`Plugin "name" installed but left disabled`, but it exits `0` either way, so a script that
only checks the exit code cannot tell the two outcomes apart. Always pass `--yes` when you
are not at a terminal.
{{% /notice %}}

## Flags

Every flag, with its environment variable, is in
[Chat flags]({{% relref "reference/cli-reference" %}}#chat-flags).
