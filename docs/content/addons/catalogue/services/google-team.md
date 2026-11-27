+++
disableToc = false
title = "Google team"
description = "Use one Google Calendar and Tasks account with a team."
+++

The Google team add-on connects a Google account's primary calendar and task
lists. The host holds and refreshes the OAuth tokens. Its connection may be
shared with named LocalAI users; creating a team member or linking a LocalAI
user ID does **not** grant that person access.

## Setup

1. Enable the Google Calendar API and Google Tasks API in Google Cloud. Create
   a Web application OAuth client with the callback URL shown by LocalAI. Add
   the Calendar and Tasks scopes to the consent screen.
2. Install the platform-specific add-on ZIP, open **Google team** in Add-ons,
   and approve the OAuth client ID and secret on its Settings tab.
3. Select **Connect with Google** and sign in as the shared Google account.
   Edit and save the connection to enable its `team` space, choosing read-only,
   approval for writes, or automatic writes.
4. Use the connection's Share control to grant access to specific LocalAI
   users. Each user must select the shared connection in their chat or agent.

The add-on requests full Calendar and Tasks access. Anyone with access to the
connection can read these services; a write-enabled connection can also change
them, subject to its write mode. Google may require app verification. An OAuth
app in Google's testing state can issue refresh tokens that expire.

## Team and tools

Ask a chat to create, list, update or remove team members. Each has a unique
ID, a name, an optional email and an optional LocalAI user ID. Within one
Google account, an email or LocalAI user ID can belong to at most one member.
The user ID is an association supplied by the operator, not a verified
identity or a permission. Manage actual access with LocalAI connection
sharing. Members without a LocalAI account are supported.
The `whoami` tool resolves the caller's authenticated LocalAI user ID to its
linked member, without taking a user ID from the caller.

The tools list events, task lists and tasks; create, update and delete events
and tasks; create task lists; and manage the roster. A task or event may be
assigned to multiple member IDs. The add-on writes the LiteDesk-compatible
`@litedesk:assignee=` tag and adds members with email addresses as event
attendees. Updates replace event title, time, description, location and
attendees, and task title, notes and assignments. Supply all details to keep
them. Tasks can be completed or reopened with `done`.

Event timestamps require RFC3339 with an offset, or all-day dates with an
exclusive end date. Google Tasks due dates use RFC3339. List results are paged
using `nextPageToken`. Remote content is untrusted. Writes use LocalAI's
preview and approval mechanism; a stale preview is rejected. An uncertain
write is not repeated automatically.

## Storage and limits

The roster is stored on the add-on owner node in its private data directory,
keyed by a hash of the primary calendar ID returned by Google. Users of the
same shared connection see the same roster. A different Google account has a
separate roster. Back up the add-on data when moving the owner node. A roster
is limited to 1,000 members. No token is stored by the add-on.

This add-on works directly with Google rather than keeping a local event or
task mirror. It does not import LiteDesk data, create local reminders, poll
for remote changes, send team notifications, or connect Gmail. Removing a
member does not rewrite existing Google items or revoke connection access.
