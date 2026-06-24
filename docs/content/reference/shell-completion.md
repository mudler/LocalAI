+++
disableToc = false
title = "Shell Completion"
weight = 18
url = "/reference/shell-completion/"
+++

LocalAI provides shell completion support for **bash**, **zsh**, and **fish** shells. Once installed, tab completion works for all CLI commands, subcommands, and flags.

## Generating Completion Scripts

Use the `completion` subcommand to generate a completion script for your shell:

```bash
local-ai completion bash
local-ai completion zsh
local-ai completion fish
```

## Installation

### Bash

Add the following to your `~/.bashrc`:

```bash
source <(local-ai completion bash)
```

Or install it system-wide:

```bash
local-ai completion bash > /etc/bash_completion.d/local-ai
```

### Zsh

Add the following to your `~/.zshrc`:

```zsh
source <(local-ai completion zsh)
```

Or install it to a completions directory:

```zsh
local-ai completion zsh > "${fpath[1]}/_local-ai"
```

If shell completions are not already enabled in your zsh environment, add the following to the beginning of your `~/.zshrc`:

```zsh
autoload -Uz compinit
compinit
```

### Fish

```fish
local-ai completion fish | source
```

Or install it permanently:

```fish
local-ai completion fish > ~/.config/fish/completions/local-ai.fish
```

## Usage

After installation, restart your shell or source your shell configuration file. Then type `local-ai` followed by a tab to see available commands:

```
$ local-ai <TAB>
run              backends         completion       explorer         models
federated        sound-generation transcript       tts              util
```

Tab completion also works for subcommands and flags:

```
$ local-ai models <TAB>
install  list

$ local-ai run --<TAB>
--address          --backends-path    --context-size     --debug            ...
```
