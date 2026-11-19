+++
disableToc = false
title = "Shell completion"
weight = 50
+++

`local-ai` can print a completion script for bash, zsh and fish. Tab then completes commands, subcommands and flags.

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

Or install it system-wide, which needs root:

```bash
local-ai completion bash | sudo tee /etc/bash_completion.d/local-ai > /dev/null
```

### Zsh

Add the following to your `~/.zshrc`:

```zsh
source <(local-ai completion zsh)
```

Or install it to a completions directory. The first folder of `fpath` can be read-only, so pick one you can write to:

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
```

Tab lists the commands and then completes subcommands and flags:

```
$ local-ai models <TAB>

$ local-ai run --<TAB>
```

Run `local-ai --help` for the commands of your build.
