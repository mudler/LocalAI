+++
disableToc = false
title = "SSH in chats and agents"
weight = 25
+++

The SSH add-on lets your [chats]({{% relref "/features/chats" %}}) and [saved agents]({{% relref "/features/chats-agents" %}}) run commands on a remote machine over SSH. By default **a person confirms each command**: the card shows the host, the user and the exact text before anything runs.

A chat can answer questions such as:

- "How full is the disk on the build server?"
- "Show the last 50 lines of the service log."
- "Restart the worker." (a change: it waits for your confirmation)

The add-on runs **one non-interactive command** per call. It has no terminal, no input and no environment variables chosen by the model. It has no interactive shell, no file transfer, no port forwarding, no agent forwarding and no jump hosts.

## Set it up

1. Install the **SSH** add-on from the add-on gallery and open its page.
2. On the machine, make a user just for LocalAI, with only the rights the task needs. A command can change or delete anything that user may touch.
3. Make a key pair for that user and add the public key to its `authorized_keys`, or choose a password. A key with a passphrase is not supported.
4. On the add-on page, choose **New SSH connection**. Type the host (with the port if it is not 22), the user name and the key or the password.
5. Press **Check host key**. The page reads the key the machine presents and shows its fingerprint. Compare it with the one on the machine (`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`), tick that you trust it and save.
6. Press **Test**: the row shows the user and the system name if the login works.

One connection is one host. For a second machine, add a second connection. A chat uses the connections you enable for it in the **Tools** menu (see [The connections of an add-on]({{% relref "/features/chats" %}}#the-connections-of-an-add-on)). With two or more enabled, the model names the connection in each call.

### The secret

The connection keeps one sealed secret. It is either a private key in PEM or OpenSSH format, or `user:password`. For a password the host splits it at the first colon, so the user name has no colon and the password may have colons. This is a v1 format: when the host gets a credential kind with named fields, the add-on will move to it and you will enter the secret again.

For a key, the user name is the one in the address of the connection. The page asks for the secret again when you change the user or the host, because the saved one belongs to the old ones.

The secret is never in a result, an error, a log or a preview. The add-on also removes it from command output, in whole and in pieces. It refuses a command that holds the secret, so a model cannot ask the machine to echo it or write it to a file.

### How the address is kept

The connection lists one allowed address: `user@host:port#sha256=<host key fingerprint>`. Every tool takes a `host` argument. LocalAI compares it with the address on the connection before the add-on is called and refuses any other, so a model cannot send the secret to another machine. The add-on dials only that address.

## The host key

The add-on trusts a host key on first use, and you approve it when you save the connection. The page reads the key without sending any secret, shows the fingerprint, and the connection stores it as a pin (the SHA-256 of the key, as `ssh-keygen -l` shows it).

- The key is checked during the key exchange, **before** any password or key signature is sent. A machine that presents another key never sees the secret.
- If the key changes, every call is refused with a message that names both fingerprints. A changed key can mean the machine was reinstalled, or that someone is intercepting the connection.
- There is no tool or setting that accepts a new key for the model. Only you can trust a new key, by editing the connection on the page and checking the host key again.
- A connection without a pin never connects.

## The mode

Each connection has a mode, set on the page by you and never by a model:

| Mode | What happens to `run` |
|---|---|
| **Off** | `run` is refused. The connection only checks the login. A connection made through the API with no mode starts here. |
| **Asks you first** | Each command waits in your approvals. The card shows the host, the user, the exact command, the time limit and the host key. A new connection made on the page starts here. |
| **Runs at once** | The command runs as soon as the chat calls it, with no card. |

Choose **Runs at once** only for a host and a user where a wrong command does no harm. In that mode the model is told to say plainly which command ran. The mode is the host's write mode of the connection (see [Confirming a held write in the chat]({{% relref "/addons/contract-reference" %}}#confirming-a-held-write-in-the-chat)): the tool declares no approval of its own, so the connection decides. In a messaging chat, the usual rules for who may confirm apply.

## Tools

| Tool | Kind | What it does |
|---|---|---|
| `get-info` | read | Logs in with the pinned key and returns the host key fingerprint, `uname -a` and the user name. |
| `run` | write | Runs one command. Arguments: `command` (at most 4000 characters) and `timeoutSeconds` (default 30, at most 45). |

`run` returns the exit code, standard output and standard error. Each stream is cut at 32 KiB, and a flag (`stdoutTruncated`, `stderrTruncated`) says when it was cut. A command that does not finish within the time limit gets its connection closed; the result says `timedOut` and holds the output so far, and the command may still run on the machine.

The limit is 45 seconds, not more, because the host stops a write call after 60 seconds and the login takes part of that.

The page also uses a third tool, `scan-host-key`, to read the host key for you. No model calls it.

## Limits and safety

- **One command at a time per connection.** A second call is refused while one runs.
- **Never twice.** The add-on notes a command before it sends it, and a repeat of the same operation answers from that note. If the connection drops after the command started, the result is "unknown" and the add-on does not send it again.
- **Output is data.** Results are marked untrusted, and the skill tells the model never to follow instructions in them.
- **Errors have their own words.** The host cannot be reached, the login was refused, the host key changed, no host key is pinned, and the host would not run a command are different messages.
- **The add-on reaches any address a connection lists**, including addresses on your own network and on the LocalAI machine itself. The secret of the connection is the only limit on what a command can do there.
