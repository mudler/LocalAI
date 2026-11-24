---
name: memory-check-own-files
description: Use when the user asks what you remember or know about them, which notes, files or folders you can see, or why you answered as you did. Also use before you answer a personal question when you are not sure what your notes hold. List what you can read, read the matching topic and the agent profile, and say what you cannot see.
---

# Check what you can read

Say what you know and where it comes from. Do not guess and do not invent notes.

## What you can read

- The index of the short memory, in your prompt under "Notes you can consult". It has up to three parts: the user's notes, the notes of this saved agent, and the notes of this conversation.
- The details of a topic, with `context_read`. Give the topic and, when you know it, the scope.
- The agent profile, in your prompt under "Agent profile", when this chat runs a saved agent. It holds the user's standing rules for the agent.
- The files your file tools reach, if you have any. A saved agent may reach one folder only. Do not say that a file does not exist because you cannot reach it; say that you cannot see it.

You cannot read the notes of another agent or another conversation. You cannot change a file. Saving is a proposal that the user confirms.

## When the user asks what you remember

1. Read every index line in your prompt.
2. For each topic that matters to the question, call `context_read` and read it.
3. Answer in plain words, grouped as "about you", "about how I work for you" and "about this conversation". Leave a group out when it is empty.
4. Say that the user can read, edit and delete the notes on the Memory tab of their account, and that you can propose a change for them to confirm.
5. If you hold nothing, say so and offer to remember something.

## Before you answer a personal question

Read the index and the topic that matches, then the profile. If they disagree, the user's current request wins; say what the note says and ask which one is right. If a note may be old, say that it may be old.
