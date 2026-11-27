+++
disableToc = false
title = "Add-ons: user guide"
description = "Find, install, turn on, update and remove add-ons."
weight = 10
+++

Add-ons give LocalAI new tools, pages and services: a mailbox, office files, a sandbox, a calendar. Open **Add-ons**, pick one and install it.

{{% callout style="warning" %}}
An add-on runs as the operating system user that runs LocalAI. Nothing confines it. Install only add-ons you trust. An add-on that you install from a local file is not signed.
{{% /callout %}}

{{< diagram "addons-permissions" "Add-ons: an admin installs and turns them on, users with the add-ons feature use them, each person connects their own accounts. An add-on can add tools, skills, pages and services, and it runs as trusted code." >}}

## Who can do what

| Task | Who |
|---|---|
| Install, update, remove, change the library | An administrator. With authentication off, you are the administrator. |
| Install a plugin that runs no code for yourself | A user who has the `plugins` feature (on by default), while personal installs are allowed. See [Plugins](#plugins). |
| Use an installed add-on | A user who has the `addons` feature. It is off by default for new users on a server with accounts. The user then switches it on in a chat (see below). |
| Connect your own account to a service, such as a mailbox | Each user, for their own account. An administrator approves the service client first. |

## Find and install

1. Sign in as an administrator and open **Add-ons**.
2. Choose **Find add-ons**. The list is the library for this operating system and architecture. Which add-ons you see depends on your build (see [The library, sources and channels](#the-library-sources-and-channels)).
3. Pick an add-on and choose **Install**. To install several, tick **Add to queue** on each card, then choose **Review and queue**.
4. The dialog lists what the add-on may do on the host. Accept it there. The add-on goes to the install queue, which downloads and installs it in the background and shows its progress.
5. When the install ends, turn the add-on on in **My add-ons**. The install never turns it on.

The install checks the package digest and the signature against the policy of the library before anything runs. The add-on then shows in **My add-ons**. Use **Show in sidebar** to pin it.

A fresh install has no add-ons. **Find add-ons** reads the official library over the network. Without a network, mirror the packages in a private library or install a zip file.

In a container, keep the `addons` folder of the data path on a volume, or you must install the add-ons again when the container is replaced. For example, set `LOCALAI_DATA_PATH=/data` and mount a volume on `/data`.

## The library, sources and channels

LocalAI registers one library, `official`, on the first start. Which index it reads depends on the build.

- A release build reads the index attached to that release. Its add-ons are fixed versions, built and signed with that release.
- Any other build, such as a source build or a development build, reads the index on the master branch. Those add-ons are in the `development` channel, and they change over time.

The **Sources** tab shows all libraries. An administrator can add another library, turn one off or remove it. When two libraries list the same add-on, a library that you added wins over a default. Two that you added conflict, and LocalAI refuses to install that add-on until you resolve it.

A library that cannot be read does not hide the others. **Find add-ons** lists the add-ons of every library that loaded and shows a notice with the name and the reason of each library that failed. The **Sources** tab shows the same reason on the row of that library, and the time of its last good refresh. Fix the address or the credential, then choose **Refresh** on the row. The add-ons of a library that failed are not listed until it loads again.

## Install from a file

Choose **Install from file** and pick a zip. Nothing signs a local file, so LocalAI cannot tell who made it. The dialog shows the checksum, the version and the permissions the add-on asks for, and the note "Local file, unsigned". You can check a file first with `local-ai addons inspect <file>`.

## Use an add-on in a chat

An installed add-on isn't on in every chat. Switch it on in the **Tools** menu of a chat. A chat uses the connections you switch on for it in the same menu. A saved agent gets only the add-ons you name in the agent. When an add-on can change something, such as send mail, the write mode decides whether LocalAI asks you first. The write mode a new connection starts with depends on the add-on, so check it when you save the connection.

## Settings and connections

Open the details of an add-on to see what it can do, its settings and its connections. An add-on that talks to a service, such as Gmail or Telegram, needs a connection. Each catalogue page says what its connection holds. For a service that uses an OAuth login, an administrator saves the client ID and secret once, and registers the redirect address with the service. Then each person signs in with their own account. The host keeps the tokens. The add-on never stores them.

## Update and remove

When a newer version is in the library, the row shows **Update**. An upgrade starts the new version, and if it does not become ready, the old one starts again. To remove an add-on, open the menu of its row and choose **Remove**. Removing an add-on keeps its data.

## Plugins

The **Plugins** tab of the Add-ons page lists plugins in the Claude Code plugin format: skills, slash commands and connections to remote MCP servers that a plugin bundles. They are not add-ons and run nothing on the server.

1. Open **Add-ons**, then **Plugins**. **Browse** shows the plugins of the marketplaces your administrator added; the curated `localai` marketplace is there by default.
2. Choose **Install** on a plugin. LocalAI fetches it and shows a review: "Community plugin, not signed", the commit, what it adds, and the hosts it connects to. Confirm to install it for yourself. A plugin that runs code is installed by an administrator, for everyone.
3. The install runs in the background. The tab tells you when it shows under **Installed**, and your notifications say how it ended.
4. In a chat, the plugin's skills are in the **Skills** menu under **Plugin: `<name>`**, its commands are in the `/` menu as `/<name>:<command>`, and its connectors have one switch in the **Tools** menu. Switch a plugin off for one chat there.

A plugin installed for everyone starts off for you: turn it on under **Installed**. When an update adds code, the plugin stays off until you choose **Turn on again**. See [Plugins]({{% relref "/features/plugins" %}}) for the details.

## Troubleshooting

| You see | Do this |
|---|---|
| A strip at the top says an add-on failed to start | Choose **Set up** or open **Details**. The reason is there. Other add-ons keep running. |
| "This add-on needs a newer LocalAI" | The add-on needs a feature that your version does not have. Update LocalAI. |
| Install from file is refused with `locked_by_deployment` | Sign-in is off or the grants are locked. Turn on sign-in, or set `--addon-host-ui-grants-without-auth` knowingly. |
| A notice says a library could not be read | The reason is on its row in the **Sources** tab. Check the address and the credential, then choose **Refresh**. The other libraries keep working. |
| No add-ons in the list | Check that the official library is on, that `--addons-enabled` is not false, and that the server can reach the network. |

For the full specification, see the [contract reference]({{% relref "/addons/contract-reference" %}}).
