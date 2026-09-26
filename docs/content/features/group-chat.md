+++
title = "Group chat"
weight = 23
toc = true
description = "Moderate a shared conversation between installed chat models"
categories = ["Features"]
+++

Group chat lets you moderate a text conversation between 2–6 participants.
Each participant uses an installed chat model and has a unique display name.
You can use the same model for multiple participants.

## Start a conversation

1. Open **Chat** in the web UI and select **Group chat**. The page is also available at `/app/group-chat`.
2. Select a model under **Model to add**, then select **Add participant**.
3. Add at least two participants. Give each participant a unique, nonempty name.
4. Enter an **Objective**, such as “Compare three ways to reduce our application's startup time.”
5. Optionally enter a **Moderator message** and select **Add message**.

Participants and the objective remain editable until the first model turn.
After that turn starts, select **New conversation** to unlock setup and clear the transcript.
The page uses the existing chat permission and chat completions API.

## Control turns

Select **Give [name] a turn** to request one response from that participant.
Select **Run one round** to give every participant one turn, in the displayed order.
To repeat that order, set **Number of rounds** to 1–10 and select **Run rounds**.
You can add moderator messages between runs.

Each request includes the objective, the participant's identity, and all completed contributions with their speakers' names.
Other participants' responses appear as shared context, not as the current model's previous assistant responses.
Private reasoning is neither displayed nor included in later requests.

The page sends one request at a time. It does not generate responses concurrently.
Sequential requests avoid simultaneous generation, but do not unload models or guarantee that only one model remains in memory.
Configure model loading and memory limits on your LocalAI server as needed.

## Stop or recover from an error

Select **Stop** to cancel the active request and all remaining turns in the run.
A server error, malformed or truncated stream, empty response, or output limit also stops the run.
Partial responses remain visible as **Incomplete** and are excluded from future requests.
You can resume with another turn or start a new conversation.

## Limits and history

{{% notice note %}}
History exists only while this page is open. Leaving the page or reloading clears it and cancels any active request.
Group chat does not save conversations or support attachments, tool calls, or automatic speaker selection.
{{% /notice %}}

Each request includes the full completed transcript. Long conversations can exceed a model's context window.
Start a new conversation when the discussion becomes too long for your selected models.
Select **Back to Chat** to return to normal chat.
