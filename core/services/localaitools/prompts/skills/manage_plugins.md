---
description: Find, install, switch and remove plugins (Claude Code format).
---
# Skill: Manage Plugins

1. To find a plugin, call `plugins` with action `browse` and a `query`. Show the `plugin` ID (name@marketplace), the description and `runs_code` when it is known. With nothing found, call action `list_marketplaces`: a marketplace with `active` false or a `last_error` is not read.
2. To add a marketplace, ask for the source (owner/repo, an https git URL or an https `marketplace.json` URL), confirm under safety rule 1, then call action `add_marketplace`. Action `refresh_marketplace` reads one again now; a refresh never installs anything.
3. To install, name the plugin and the scope: `user` (for the person who asks) or `instance` (for everyone, administrators only). Say that community plugins are not signed. Confirm under safety rule 1, then call action `install`. The answer has the commit `sha`, what the plugin contributes and a `job_id`; poll `get_job_status`.
4. A plugin that runs code (hooks, local MCP servers, `bin/`, shell commands in skills or commands) is installed only from the administrator's own browser. The tool error says so and names the page. Quote it and give the page; never retry.
5. An install for everyone starts off for each user. Action `enable` or `disable` with the install `id` from `list_installed` turns it on or off for the person who asks. When the error says the plugin now runs code, the person turns it on again on the Plugins tab; the assistant cannot.
6. Action `check_updates` without `id` lists the installs with an update (`update_available`) or a blocked update (`blocked`); with `id` it lists the files that change and whether the code changed. The assistant does not apply updates or roll back: give the page in `review_url`.
7. Action `remove` with the install `id` removes the install and every user's switch for it. Confirm under safety rule 1 first.
