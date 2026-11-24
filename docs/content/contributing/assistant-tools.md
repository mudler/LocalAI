+++
disableToc = false
title = "Add a tool or a skill to the LocalAI Assistant"
description = "Where the tools and the skill prompts of the LocalAI Assistant live, and what to change to add one."
weight = 50
devbuild = "feature"
+++

Add a tool or a skill to the LocalAI Assistant. This page is for contributors. The tools that an administrator uses are in [LocalAI Assistant]({{% relref "/features/chats-and-agents/localai-assistant" %}}).

## Adding new tools or skills

The MCP server lives at `core/services/localaitools/`. Tools are registered in `tools_*.go`; skill prompts (the markdown the LLM sees) are embedded from `prompts/`: one file per recipe under `prompts/skills/`, sent inline by the standalone server and offered as a skill by the chats. To add a new admin tool, add a method to the `LocalAIClient` interface plus implementations in `inproc/` and `httpapi/`, then register the tool and add or update a skill prompt. See `.agents/localai-assistant-mcp.md` for the full contributor checklist.
