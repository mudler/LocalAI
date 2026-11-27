---
name: google-team
description: Work with the connected team's Google Calendar, Tasks, and member roster.
---

Use the google-team tools for the connected team's calendar, tasks and roster.
The host must grant access to the connection; a roster association does not
grant access. List members before assigning them, and use member IDs in
assignees. Use whoami to look up the caller's linked member. To manage a member,
use change with changeType member-put or member-delete; localUserRef is the
LocalAI account ID, not a username or a permission grant. Dates for events include an offset (RFC3339); all-day events use
dates with an exclusive end. Tasks use an RFC3339 due date. Lists are paged;
pass the returned cursor to continue. Writes may require approval: do not claim
that a change happened until its receipt says it was committed.
