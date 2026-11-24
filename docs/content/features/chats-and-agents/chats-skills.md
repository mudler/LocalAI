+++
url = "/features/chats-skills/"
disableToc = false
title = "Slash commands and skills"
description = "Run slash commands, apply skills, hand tasks to saved agents and write skills of your own."
weight = 17
devbuild = "feature"
+++

Run a slash command from the message box, apply a skill to one message, hand a task to a saved agent, and write skills of your own. A skill is a folder with a `SKILL.md` that the model reads when a task fits.

## Slash commands

Type `/` in the message box of a chat, or on Home, for the list of commands.
Typing filters it; arrows move, Enter runs the command, Tab completes it, and
Esc closes the list. A command that takes an argument lists the choices as you
type it: `/agent ` lists your saved agents, `/skill ` the skills of the chat
and `/model ` the chat models, `/loop stop ` the loops of the chat.

| Command | What it does |
|---------|--------------|
| `/model`, `/models` | Open the model list; with a name, switch to that model. |
| `/new`, `/chats` | Start a new chat, or open the list of your chats. |
| `/find`, `/settings`, `/export`, `/delete` | Search this chat, open its settings, download it as Markdown, delete it. |
| `/effort` | Open the reasoning effort menu of this chat. |
| `/<skill>`, `/skill <name>` | Run the message with one of the skills of this chat (see [Run a skill](#run-a-skill)). |
| `/skills` | Open the Skills page, where you write and manage skills (see [Skills](#skills)). |
| `/agent <name> <task>` | Hand the task to one of your saved agents (see [Hand a task to a saved agent](#hand-a-task-to-a-saved-agent)). |
| `/compact` | Compact the context now (see [Compact or clear the context yourself]({{% relref "/features/chats-and-agents/chats-long-conversations#compact-or-clear-the-context-yourself" %}})). |
| `/clear` | Clear the context: a fresh start in the same chat (see [Compact or clear the context yourself]({{% relref "/features/chats-and-agents/chats-long-conversations#compact-or-clear-the-context-yourself" %}})). |
| `/goal`, `/goal <text>`, `/goal clear`, `/goal resume` | Show, set, remove or resume the goal of the chat (see [Goals]({{% relref "/features/chats-and-agents/chats-goals#goals" %}})). `/goal ` lists `resume` and `clear` for the goal the chat has. |
| `/loop [interval] <prompt>`, `/loop list`, `/loop stop [id]` | Repeat a prompt in this chat, list its loops, stop one or all (see [Loops]({{% relref "/features/chats-and-agents/chats-loops#loops-repeat-a-prompt-in-a-chat" %}})). |
| `/attach`, `/attach clear` | Choose files to send, or remove the files waiting to be sent. |
| `/help` | List every command with what it does. |
| `/about` | Show the model, whether it is loaded, how many tools and skills the chat has, where its commands run and the size of its context. |

Home offers the same commands, less `/compact`, `/clear`, `/goal`, `/loop` and `/about`: a chat
that does not exist yet has no context. `/<skill>` and `/agent` work there
because the chat is created with the switches you chose first.

### Run a skill

Every skill the chat can use is a command of its own: `/review check the
diff` sends "check the diff" with the `review` skill. The list marks where each
skill comes from (built-in, add-on, shared or yours). A skill whose name is a
command, such as a skill named `new`, is reached with `/skill new`. A skill
that is switched off, replaced or for another user is not offered.

The message goes with the name of the skill. The model gets the text of the
skill's `SKILL.md` before your message, for this turn only; the transcript
keeps your words and shows the skill as a chip. A regenerate runs the skill
again.

### Hand a task to a saved agent

`/agent Reviewer check the draft` asks the model to start the saved agent
`Reviewer` as a sub-agent with the task (see
[Saved agents as sub-agents]({{% relref "/features/chats-and-agents/chats-sub-agents#saved-agents-as-sub-agents" %}})). The chat must have
**Saved agents** switched on in its Tools menu; while it is off, the list offers
to switch it on.

A command message starts a turn: while a turn runs, send it again when the
model has answered. The API of these messages is in
[Messages sent with a skill or a saved agent](#messages-sent-with-a-skill-or-a-saved-agent).

### Messages sent with a skill or a saved agent

A message can carry `skill`, a skill of the chat, or `agent`, the id or the
name of one of your saved agents. With a skill, the model gets the text of the
skill's `SKILL.md` before your message, for this turn only; the transcript
keeps your words, and the line has `"invoked":{"kind":"skill","name":"review"}`.
With an agent, the model is asked to start that saved agent as a sub-agent
with the task (the chat must have **Saved agents** switched on). A regenerate
runs the skill again.

```bash
curl -X POST .../api/chats/3f1c.../messages -H 'Content-Type: application/json' -d '{"text":"check the diff","skill":"review"}'
curl -X POST .../api/chats/3f1c.../messages -H 'Content-Type: application/json' -d '{"text":"check the draft","agent":"rev1"}'
# An unknown skill: 400 with the names of the skills of the chat. An unknown
# agent, another user's, or saved agents off for this chat: 400.
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

Such a message starts a turn. While a turn runs it does not wait like other
messages: it answers 409, and you send it again when the model has answered.

## Skills

In the web UI, the **Skills** item in the sidebar, right after Knowledge, opens the page where you write, change and delete skills. Every chat can use them, and agents use the ones you add to them. You can also open the page from the **Manage skills** link at the bottom of the Skills menu in the composer, or by typing `/skills` in the message box.

A skill is a folder with a `SKILL.md`: a `name`, a one-line `description` and a procedure the model reads when a task matches. An optional `requires` key lists what the procedure needs; today the only value is `exec`, a way to run commands (`requires: [exec]`). A chat that cannot run commands lists such a skill as unavailable (see [Running commands]({{% relref "/features/chats-and-agents/chats-commands#running-commands" %}})). At each turn the chat's prompt lists the skills it has, by name and description only, and tells the model to call the `load_skill` tool with a skill's name before a task that matches it. `load_skill` returns the instructions of the skill, the text after its frontmatter. It offers exactly the skills of that list: for each name the nearest skill, not switched off, and the skills of the add-ons the chat can use now. Any other name is refused with the names of the chat's skills and the near ones first (for example `Did you mean pdf?` for `PDF`).

An optional `sandbox` key names the sandbox profile the skill works best in, for example `sandbox: media` for a skill that cuts video with ffmpeg. It is a hint and nothing more. The index marks the skill as preferring that profile. When the chat runs another profile, `load_skill` adds a line that tells the model the skill wants the named profile, and the model may offer to switch with `sandbox_profile` (action `use`). It never switches by itself and nothing restarts the sandbox. If a tool is missing, the model says which profile has it. A value that is not a profile ID is ignored.

```yaml
---
name: cut-clips
description: Cut a video into clips with ffmpeg.
sandbox: media
---
```


When the folder of a skill holds other files, such as references and scripts, the answer starts with `Base directory for this skill:` and the folder, and names up to 20 of the files, relative to it, so the model reads them with `read_file`. A sub-agent that has no `read_file` gets the instructions only. A skill with `requires: [exec]` in a chat that cannot run commands answers that it cannot be used there. Every chat with at least one skill has `load_skill`, and a sub-agent gets it with its parent's other tools. It has no switch and Settings > Tools does not list it; the Skills menu chooses what it offers. The Sandbox does not see the skill folders (the built-in `bash` does, see [Commands on this machine]({{% relref "/features/chats-and-agents/chats-commands#commands-on-this-machine" %}})). When the model reads a `SKILL.md` with `read_file` at a wrong path, the error names the right path and `load_skill`. When it writes or moves a `SKILL.md` with `write_file` or `move_file` into a folder that is not a skills folder (for example `/workspace/home/skills/<name>/SKILL.md`, without the dot), the file is not loaded as a skill, and the result of the call says so and names `/workspace/home/.skills/<name>/SKILL.md`, so the model moves it there. A `SKILL.md` over 64 KiB is refused.

Skills come from seven places, nearest first, and the nearest wins when two have the same name:

| Layer | Where | Who writes it |
|---|---|---|
| Folder | any `.skills` folder in your files, except under `Projects/` | you |
| Project | `Projects/<project>/skills` in your home folder | you |
| Agent | `LocalAI/agents/<agent>/skills` in your home folder | the agent proposes, you confirm |
| Personal | `.skills` in your home folder | you |
| Shared | for every user | an administrator |
| Add-on | ships with an installed add-on | the add-on owner |
| Built-in | ships with the server | the server |

The project layer holds the skills of one project: `Projects/<project>/skills/<name>/SKILL.md`. Every chat and every agent of the project gets them, with no grant and no switch to set first, and a skill you add reaches a chat that already exists on its next turn. They win over your personal and agent skills of the same name; only a skill in a `.skills` folder next to the files you work in is nearer. The project's skills limit (the skills list of its defaults) does not apply to them. Other chats never see them: a `.skills` folder below `Projects/` isn't loaded as a folder skill, and the skills of an older `Projects/<project>/.skills` folder are copied to `skills/` once when the server starts (the old folder is kept). Only you write them: through the project page, or the skills endpoints with `layer=project` and `project=<project id>` (`GET /api/chats/skills?layer=project&project=<id>` lists them, `POST` with `"layer": "project", "project": "<id>"`, and `GET`, `PUT` and `DELETE /api/chats/skills/project/<name>?project=<id>`). The folder is a place LocalAI manages, so it cannot be shared alone. When you share the project folder with **Can edit**, the project's skills are off, because another person could write them: the calls answer 409, and `GET /api/chats/projects/<id>` says `skills_blocked: true`. A switch for a project skill has the id `project:<name>`.

The agent layer holds the skills of one saved agent. Only a chat that runs that agent sees them, and it needs no grant in the agent's skill list. They win over your personal skill of the same name, for that agent only. An agent writes one by a change proposal of kind `skill` with scope `agent` (see [Agents]({{%relref "/features/chats-and-agents/chats-agents" %}})); nothing is written until you confirm, and the write can be undone. An agent skill cannot take the name of a built-in skill or of a document skill such as `xlsx`, and a skill that you locked in your personal layer stays locked against it. The grant `skills.write` shows the built-in `writing-skills` to the agent, led by the line "Do not write files. Call propose_change with kind skill." The **Skills** page lists agent skills with the agent's name and a **Remove** button; a removed skill goes to the Trash. `GET /api/chats/skills?agents=true` adds them to the list with `layer: "agent"`, `agent` and `agent_name` (a chat's own list has the layer of its agent already), and `GET` or `DELETE /api/chats/skills/agent/<name>?agent=<id>` reads or removes one.

The add-on layer holds the skills of the add-ons that the chat can use now: installed, running, usable by you and on for the chat (see [Add-on tools]({{% relref "/features/chats-and-agents/chats-tools#add-on-tools" %}})). A chat with a saved agent gets them only for the add-ons the agent grants. The model loads one by its name with `load_skill`; its files are in `/workspace/shared-addon-skills/<add-on>/<skill>/`, where the add-on ID says where it comes from. Switching the add-on off for a chat takes its skills out of that chat's index, and the skill list shows each one with `addon` and `addon_title`. Its switch id is `addon:<name>`.

An add-on can also replace a built-in skill of the same name, for example the Office add-on's own `xlsx`, written for its tools. The add-on's skill then serves and the built-in one shows `shadowed: "addon"` with `shadowed_by`, the add-on's ID. When the add-on is off for the chat or not running, or you switch its skill off (`addon:xlsx`), the built-in skill serves again: the model is told in one line, and the built-in skill in the skill list carries `fallback`, the add-on's title. When several add-ons replace one skill, the one installed first serves until an administrator chooses another in Settings > Tools, under **Skills that add-ons replace**, or with the switch in the add-on's details on the Add-ons page.

In the **Skills** menu over the message box each skill says where it comes from: **Built-in**, the add-on's title, **Personal**, **Shared** or **Workspace**. A built-in skill that an add-on replaces is listed under the others, dimmed, with "Replaced by <add-on>" and no switch. When a built-in skill serves because the add-on's own skill is off or not running, one line under the title says so. The **Skills** page lists add-on skills under the **Add-on** layer, with the add-on's title as the badge, and says which skill replaces which. See [Skills of an add-on]({{% relref "/addons/contract-reference/manifest#skills" %}}) for how an add-on ships them.

The format is the same as the skills LocalAI already used, so an existing skill carries over by copying its folder into one of the places above. Skills in the public [Agent Skills](https://agentskills.io/specification) format can also be imported from a zip file and exported as one (see [Import and export skills](#import-and-export-skills)). Git sources are not part of chats.

```bash
curl -X POST .../api/chats/skills -H 'Content-Type: application/json' -d '{"layer": "personal", "name": "review", "description": "Check a change before it ships", "body": "Read the diff.\nSay what you find."}'
curl .../api/chats/skills?chat=3f1c...            # what the chat has, with its switches
curl -X PUT .../api/chats/3f1c.../skills -H 'Content-Type: application/json' -d '{"off": ["review"]}'   # switch one off for this chat
```

A chat keeps its own switches. Turning a skill off takes it out of the index at the next turn. A switch with the plain name turns off the skill that wins, so a skill of the same name further away shows through, as the list says. A switch with the id `layer:name` (the `id` in the skill list, for example `personal:review`) turns off the skill of that layer only. A deleted skill goes to the Trash like any file. Only an administrator writes shared skills, and everyone reads them. Your personal skills are as private as your files.

The **Skills** page lists them by layer and edits personal skills, and the **Skills** menu over the message box switches them per chat. The menu has one switch for each skill, with its name and a line of its description (the full text shows as a tooltip), the count of skills that are on ("12 of 20 on"), and **Select all** and **Disable all**. From nine skills on it also has a search field; while a search is on, the two buttons read **Select shown** and **Disable shown** and change only the skills in the list. Every change is one request, and a change the server refuses goes back with a message under the list.

A chat stores the skills that are **off**, not the skills that are on. So **Select all** sends an empty list, **Disable all** sends the ids of every skill in the list, and a skill you add later is on in every chat, also in a chat where you chose **Disable all**. The menu says so under its title.

The server ships these built-in skills: `writing-skills`, `localai-workspace`, `localai-apps`, `localai`, `assemble-media` and `media-prompting`.
`localai` teaches the model the [LocalAI client tools]({{% relref "/features/chats-and-agents/localai-client-tools#client-tools" %}}): when to use each one, how to clone a voice and what to say when a model is missing or a permission is refused. A chat lists it only while it has the **LocalAI client** group, so it costs nothing when the group is off. An add-on cannot replace it, as for the other skills that describe LocalAI itself.
An administrator's chat that has the **LocalAI admin** group also lists one skill per admin recipe, named `localai-admin-<recipe>`, such as `localai-admin-install-chat-model`. Nobody else sees them. See [the prompt and the recipes]({{% relref "/features/chats-and-agents/localai-assistant#the-prompt-and-the-recipes" %}}).
`localai-workspace` describes changes to your persona, profile, shortcuts, Home widgets, and theme;
see [Make LocalAI yours]({{% relref "/features/chats-and-agents/make-localai-yours" %}}).
`localai-apps` describes how to build an app, tracker, or tool as a [Page]({{%relref "/features/pages-and-home/pages" %}}).
`assemble-media` is for the sandbox. It has `ffmpeg`, `sox` and ImageMagick recipes to join, trim, mix, subtitle and inspect generated pieces, and to make frames and inpainting masks for the next generation. It can also build a short video from frames drawn with Python or SVG, such as a title card or a slideshow with a slow zoom. Look at the result before you use it. It carries a hint for the `media` sandbox profile.
`media-prompting` teaches the model how to prompt the image, video, music and speech models: a loop of pick, read the card, generate, inspect and change one thing, the anatomy of a prompt for each kind, what steps, guidance, size and seed do, and what to refuse. It has one card per model family in its `cards/` folder (Qwen-Image, MiniMax-H3, YuE2 and Qwen3-TTS), which the model reads when it uses that family. To add a family, add a card and a row in the table of `SKILL.md`.

### Import and export skills

LocalAI reads skills in the public [Agent Skills](https://agentskills.io/specification) format: a folder with a `SKILL.md` (frontmatter with `name` and `description`, and the optional `license`, `compatibility`, `metadata` and `allowed-tools`) and, optionally, `scripts/`, `references/` and `assets/`. On the **Skills** page, **Import** takes a zip file and **Export** (on one of your personal skills) gives one back. Imported skills go to your personal layer, as with `POST /api/chats/skills/import`.

The zip holds either one skill (`SKILL.md` at the top) or one folder per skill. The server checks the whole archive first and stores nothing when any part is not valid:

- `name` has 1 to 64 lowercase letters, digits and single hyphens, with no hyphen at the start or the end, and equals the folder name. `description` has 1 to 1024 characters. `compatibility` has at most 500 characters. `metadata` maps text to text. LocalAI reads the names as ASCII only, which is stricter than the specification.
- A path with `..`, a leading `/`, a backslash or a drive letter, a link, a device, an encrypted entry, two names that differ only in case, and a file and a folder with one name are refused.
- At most 500 entries, 5 MiB for a file and 20 MiB in all, counted on the bytes that are really unpacked.
- The `SKILL.md` is at most 64 KiB, as for every skill. Fields that the specification does not name are kept in the file and ignored.

A name that you already use is refused with `409`; an import never replaces a skill, so delete or rename the old one first. The name of a skill that ships with LocalAI (a built-in skill, a document skill such as `xlsx`) is refused with `400`, so an imported file cannot take its place.

**Scripts are stored, not run.** The import writes every file, `scripts/` included, as a plain file and runs nothing. A script is only read or run later, when a chat that has the tools for it chooses to, under the rules of that tool (see [Running commands]({{% relref "/features/chats-and-agents/chats-commands#running-commands" %}})). Skill text is data from the person who wrote it: read an imported skill before you give it to an agent.

An imported skill is listed with `new: true` and a **New** badge until a chat loads it with `load_skill` for the first time. Use the mark to find skills you have not read yet. Editing a skill in the page writes only the name, the description and the text, so the other frontmatter fields of an imported skill are dropped; edit the file in Files to keep them.

```bash
curl -X POST .../api/chats/skills/import -F file=@pdf-tools.zip      # 201 with the stored skills
curl -o pdf-tools.zip .../api/chats/skills/personal/pdf-tools/export  # one folder, same layout
```

An export of a skill imports again to the same files.

### Document skills

The familiar names `docx`, `pptx`, `pdf`, and `xlsx` identify document authoring skills from add-ons, not built-in skills.
[Sandbox]({{% relref "/addons/catalogue/work/sandbox-settings#sandbox" %}}) supplies Python/Paper procedures and their references.
[Simple Office]({{% relref "/addons/contract-reference/first-party-packages#simple-office" %}}) supplies separate procedures for its document tools, without command execution.

1. Install and enable a provider on the **Add-ons** page.
2. Switch the provider on in the chat's **Tools** menu.
3. For a saved agent, grant that add-on explicitly and select the required skill names.
4. For Sandbox procedures, select Sandbox for `bash` and allow command execution in the chat or agent.

Sandbox procedures require usable isolated commands from the selected Sandbox provider.
An installed Sandbox or a host command runner alone is not enough.
These selections never grant add-ons or command execution automatically.

With both providers eligible, the existing provider choice in **Settings > Tools** applies.
Without a choice, the eligible provider installed first serves each name.
A pinned provider that is unavailable does not switch silently to another provider.
The Skills menu shows its unavailable reason.
Folder, personal, and shared skills keep their existing precedence over add-on skills.

With neither provider eligible, LocalAI offers no built-in document authoring fallback.
Document-text extraction and the `read_file` and `search_files` tools remain available independently of these authoring skills.

Existing saved agent selections keep the same names.
Existing `builtin:docx`, `builtin:pptx`, `builtin:pdf`, and `builtin:xlsx` OFF preferences become corresponding `addon:<name>` OFF preferences.
LocalAI persists this change before selecting providers. Existing add-on and plain-name OFF preferences remain effective.
You can explicitly switch a skill on again; subsequent reads do not turn it off again.
Other skill preferences remain unchanged.

Sandbox installs PDF helpers inside its guest:
`/usr/local/lib/localai/document-skills/pdf/content_qa.py` and
`/usr/local/lib/localai/document-skills/pdf/verify_pdf.py`. For the other formats it also installs
`/usr/local/lib/localai/document-skills/pptx/pptx_guard.py` (checks that no shape collapsed to nothing,
sticks out of the slide or holds text that does not fit), a `render.sh` for each of `pptx`, `docx` and
`xlsx` (renders each slide or page to a PNG) and `xlsx/recalc.sh` (recalculates a workbook).
Rendering and recalculation need LibreOffice, which only the `office` profile has: the `pptx`, `docx`
and `xlsx` skills tell the model to switch to it before it builds a file, and to look at every image,
or check the recalculated values, before it reports the file done. The render is LibreOffice's, not
Microsoft Office's.
Run them with `python3` through Sandbox commands, not through paths in host skill folders.
Upgrades remove stale built-in document trees, but preserve user-written skills.
Use `load_skill` by name; historical physical paths are not a compatibility interface.
