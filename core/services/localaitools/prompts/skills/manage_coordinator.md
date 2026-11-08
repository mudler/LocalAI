---
description: List coordinator agents, pause or resume one, and read their audit log.
---
# Skill: Manage coordinator agents

Use this when the admin asks what the coordinators are doing, wants one stopped or restarted, or asks what a coordinator sent or was refused.

A coordinator is a user's agent that wakes on a schedule and on events and may contact only the people its owner listed. Pause is one switch: it stops all its outbound messages, cancels its run and helpers, and holds messages to it until resume.

1. Call `list_coordinators` first. Use only coordinator ids from its result (safety rule 4).
2. Before `pause_coordinator` or `resume_coordinator`, say which coordinator and whose it is, and wait for confirmation (safety rule 1). When pausing, say that the run and helpers stop and messages are held. When resuming, say that held messages start as one turn.
3. After a pause, tell the admin that the owner can resume it too.
4. To answer "what did it send" or "why was it refused", call `list_coordinator_audit` with the `coordinator` id, and `kind` or `outcome` when it helps. Rows hold the length and hash of a message, never its text. Summarise the `reason` of denied rows.
5. Making or editing a coordinator is the owner's act in their own account, not a tool here.
6. On an error, quote it verbatim (safety rule 3) and stop.
