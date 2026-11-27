# Curated plugin marketplace

This folder is the curated LocalAI plugin marketplace. Each LocalAI server adds it as the default marketplace `localai`, which it reads as one file:

```
https://raw.githubusercontent.com/mudler/LocalAI/<ref>/plugins/.claude-plugin/marketplace.json
```

`<ref>` is the release tag of a release build and `master` for any other build. User docs: [Plugins](https://localai.io/docs/features/plugins/).

| Path | What it is |
|---|---|
| `.claude-plugin/marketplace.json` | The list of plugins, in the Claude Code marketplace format |
| `marketplace.schema.json` | The JSON schema that CI checks the list against |
| `localai/` | The `localai` plugin, kept in this repository |

## Rules for an entry

- The source is `github` or `git-subdir`, with a full 40-hex `sha`. A server reads this file from a URL, so relative sources do not work. `npm`, `archive` and `command` sources are refused.
- An upstream plugin is referenced at its own repository and commit. It is never copied here.
- `metadata.localai` has:
  - `license`: the SPDX ID of the license file in the plugin folder, or at the repository root, at that commit;
  - `runsCode`: `true` when the plugin has a command hook, a stdio MCP server, files in `bin/`, an LSP server, a monitor or a shell injection;
  - `phase`: the first LocalAI phase in which the plugin works (`P0` for skills and commands that run no code);
  - `reviewed`: the date of the last review, `YYYY-MM-DD`.
- The plugin is reviewed by a person at that commit: every file read, no shell injection (`` !`cmd` `` or a ` ```! ` block) in a skill or a command, nothing that sends data to an outside service without saying so, and a license that allows the use.

## What CI checks

`.github/workflows/plugins-marketplace.yml` runs on each pull request that changes this folder:

1. The schema (`marketplace.schema.json`).
2. The pins: a supported source with a full SHA (`.github/scripts/plugins-marketplace-check.sh`).
3. The license: the license file at the SHA has the SPDX ID of `metadata.localai.license`.
4. The pin of `localai` (see below).
5. `local-ai plugins validate --strict` on `plugins/localai/`, then on every entry, fetched at its SHA with the same fetcher as the server. It fails when `runsCode` differs from what the checks find.

Run the same checks on your machine:

```bash
.github/scripts/plugins-marketplace-check.sh plugins/.claude-plugin/marketplace.json /tmp/marketplace.ci.json
local-ai plugins validate --strict plugins/localai
local-ai plugins validate --strict --marketplace /tmp/marketplace.ci.json
```

## The pin of the `localai` plugin

A commit cannot hold its own SHA. A pull request that changes `plugins/localai/` keeps the old `sha` of the `localai` entry. CI warns about it and validates the folder from the branch. After the merge, `.github/workflows/plugins-bump-localai.yml` opens a pull request that sets the `sha` to the last commit that changed the folder. Any other pull request fails while the pinned folder differs from the branch, so merge the pin pull request first.

## Propose a plugin

1. Open an issue or a pull request that names the plugin, its repository and why it helps LocalAI users.
2. In the pull request, add the entry with today's commit SHA (`git ls-remote https://github.com/<owner>/<repo>.git HEAD`) and `metadata.localai`.
3. Do the trial below with a local model and paste the results in the pull request.

## Trial with a local model

Run it on a LocalAI server built from the branch, with a chat model of 8B parameters or smaller. Add a marketplace that has the new entry (for example the raw URL of the file on your branch), install the plugin for yourself, and turn it on.

- [ ] `local-ai plugins install <name>@<marketplace>` shows `runs code: no` and no skipped component that the entry needs.
- [ ] Each skill: ask a question that should load it. The chat loads `<plugin>:<skill>` and the answer follows the skill.
- [ ] Each command: run `/<plugin>:<command> <arguments>`. The arguments reach the text, and the model does what the command says.
- [ ] No skill or command asks for a tool that LocalAI drops (for example `TodoWrite`) in a way that blocks the task.
- [ ] Turn the plugin off for one chat: its skills and commands are gone in that chat only.
- [ ] Record the model, the server commit and one line per skill and command (works, partly, fails) in the pull request.

For `localai` itself:

- [ ] `localai:api`: "Check that my LocalAI is up and list its models" gives the `/readyz` and `/v1/models` calls.
- [ ] `localai:models`: "Find a small embedding model in the gallery" gives a `/models/available` query with `jq`.
- [ ] `localai:addons`: "Why does pip install fail in the sandbox?" names the network setting.
- [ ] `/localai:install-model qwen`: lists a few matches and asks which one; with an exact name, it installs it and follows the job to the end.
