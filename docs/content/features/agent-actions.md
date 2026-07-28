+++
disableToc = false
title = "Agent actions"
weight = 21
url = '/features/agent-actions'
+++

Actions are the tools an agent can call while it reasons: search the web, open a browser, read or write a GitHub repository, generate an image, remember a fact, and so on. This page lists the actions that ship with LocalAI (through the embedded [LocalAGI](https://github.com/mudler/LocalAGI) library) and explains how to enable one on an agent.

For an end-to-end first run, see {{% relref "getting-started/first-agent" %}}. For the agent platform overview, see {{% relref "features/agents" %}}.

## Enabling an action on an agent

Actions are attached per agent, not globally.

- **In the web UI:** open the Agents page, edit an agent, and add actions in its configuration. Each action exposes its own configuration fields (for example an API token or a target repository) that you fill in when you add it.
- **In an agent config (JSON):** an agent's exported configuration lists its enabled actions and their settings, so an imported agent carries its actions with it.

Some actions need credentials or external configuration to work (a GitHub token, SMTP server details, a Telegram bot token). An action that is enabled but missing its required configuration will fail at call time, not when you add it. If an imported agent will not run, a missing action credential is one of the things to check (see the checklist in {{% relref "getting-started/first-agent" %}}).

## Discovering the current catalog at runtime

The set of built-in actions is served by the API, so it always reflects the version you are running:

```bash
curl http://localhost:8080/api/agents/actions
```

This returns the list of available action names. Two related endpoints describe and run a single action:

- `POST /api/agents/actions/{name}/definition` returns the action's parameter/configuration schema (send a JSON body with an optional `config` object).
- `POST /api/agents/actions/{name}/run` executes the action directly (JSON body with `config` and `params`), which is useful for testing an action's configuration outside an agent conversation.

## Custom actions

You can add your own actions without rebuilding LocalAI by pointing it at a directory of custom action definitions:

```bash
LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR=/path/to/custom-actions local-ai run
```

Actions found in that directory become available to agents alongside the built-in ones.

## Built-in actions

The actions below ship with LocalAI. The exact catalog can change between releases, so treat `GET /api/agents/actions` (and the Agents UI action picker) as the source of truth for the version you are running.

### Web and knowledge

| Action | What it does | Typically requires |
|--------|--------------|--------------------|
| `search` | Web search. | Search provider configuration. |
| `browse` | Browse a web page (fetch and read its content). | None. |
| `scraper` | Scrape structured content from a page. | None. |
| `wikipedia` | Look up an article on Wikipedia. | None. |

### Content generation

| Action | What it does | Typically requires |
|--------|--------------|--------------------|
| `generate_image` | Generate an image (through LocalAI's own image endpoint). | An installed image-generation model. |
| `generate_song` | Generate audio/music. | An installed audio-generation model. |
| `generate_pdf` | Produce a PDF document. | None. |

### Memory

| Action | What it does | Typically requires |
|--------|--------------|--------------------|
| `add_to_memory` | Store a fact in the agent's memory. | Agent memory/knowledge base enabled. |
| `list_memory` | List stored memories. | Agent memory enabled. |
| `search_memory` | Search stored memories. | Agent memory enabled. |
| `remove_from_memory` | Remove a stored memory. | Agent memory enabled. |

### Reminders

| Action | What it does | Typically requires |
|--------|--------------|--------------------|
| `set_reminder` | Set a reminder. | None. |
| `list_reminders` | List reminders. | None. |
| `remove_reminder` | Remove a reminder. | None. |

### Messaging and notifications

| Action | What it does | Typically requires |
|--------|--------------|--------------------|
| `send-mail` | Send an email. | SMTP server and credentials. |
| `send-telegram-message` | Send a Telegram message. | A Telegram bot token. |
| `twitter-post` | Post to X/Twitter. | X/Twitter API credentials. |
| `webhook` | Call an outbound webhook. | The target webhook URL. |

### GitHub

All GitHub actions authenticate with a GitHub token and (for most) a target owner/repository.

| Action | What it does |
|--------|--------------|
| `github-issue-opener` | Open an issue. |
| `github-issue-editor` | Edit an issue. |
| `github-issue-closer` | Close an issue. |
| `github-issue-reader` | Read an issue. |
| `github-issue-commenter` | Comment on an issue. |
| `github-issue-searcher` | Search issues. |
| `github-issue-labeler` | Label an issue. |
| `github-pr-reader` | Read a pull request. |
| `github-pr-commenter` | Comment on a pull request. |
| `github-pr-reviewer` | Review a pull request. |
| `github-pr-creator` | Create a pull request. |
| `github-readme` | Read a repository README. |
| `github-repository-get-content` | Get a file's content. |
| `github-get-all-repository-content` | Get all repository content. |
| `github-repository-list-files` | List files in a repository. |
| `github-repository-search-files` | Search files in a repository. |
| `github-repository-create-or-update-content` | Create or update a file. |

### Other

| Action | What it does | Typically requires |
|--------|--------------|--------------------|
| `shell-command` | Run a shell command. | Enable with care; grants command execution. |
| `call_agents` | Delegate to another agent. | Another configured agent. |
| `pikvm_power_control` | Control power through a PiKVM device. | PiKVM endpoint and credentials. |
| `counter` | A simple counter (example/utility action). | None. |
| `custom` | A user-defined custom action. | See [Custom actions](#custom-actions). |

## See also

- {{% relref "features/agents" %}} for the agent platform overview.
- {{% relref "getting-started/first-agent" %}} for a full walkthrough, including the "will not run" checklist.
- {{% relref "features/mcp" %}} for attaching external MCP tool servers to an agent (an alternative to built-in actions).
