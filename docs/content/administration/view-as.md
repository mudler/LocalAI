+++
disableToc = false
title = "View as another user"
weight = 31
description = "Let an administrator look at, and with a second step change, the projects of another person in the web UI."
keywords = ["view as","impersonation","users","administrator","projects","user_id","read-only"]
devbuild = "install"
+++

**View as** lets an administrator open the web UI as another person. You see that person's projects, and you can change them when you choose to. It helps when you support a team: you look at what a person sees before you change anything.

View as is in the web UI of a server with [sign-in turned on](/administration/authentication/). Only administrators can use it. It does not show when sign-in is off.

## Start and stop

1. Open **Administration**, then **Users**.
2. Press **View as** on the row of a person. You can also open the menu of the row and choose **View as**. On a narrow screen the menu is the only place.
3. Or press <kbd>Ctrl</kbd> + <kbd>K</kbd> on an Administration page, type a name, and choose **View as** *name*.

The button is on the rows of people who are active, who are not you and who are not administrators. A person who waits for approval or who is disabled has no button. Viewing another administrator is not part of View as.

LocalAI opens **Projects**. A bar at the top of every page, including Administration, says who you view. **Exit** in the bar ends View as. You cannot dismiss the bar in another way.

View as belongs to the browser tab. Another tab is not affected. If you reload the page, the tab stays on the same person and goes back to read-only.

## The two modes

| Mode | Bar | What you can do |
|---|---|---|
| **Read-only** (the start) | Accent colour, the tag **READ-ONLY**, and "Viewing as *name*. Read-only." | Read. Controls that change data are dimmed. They say "Allow changes to edit" and do nothing. |
| **Acting** | Error colour, a warning icon, the tag **ACTING**, and "Acting as *name*. Changes are made on *name*'s account and are logged." | Create, change and delete, on the account of the person. |

Turn on **Allow changes** in the bar to act. LocalAI asks you to confirm first. The first choice in the question is **Stay read-only**. Turn the switch off to go back to read-only; this needs no confirmation.

Acting goes back to read-only by itself after 15 minutes without input. Reload and **Exit** also end it.

Each change you make while you act is logged on the server with you as the actor and the person as the account. See [Act for another user](/features/chats-and-agents/projects-reference/#act-for-another-user).

## Which pages follow

A page that **follows** View as shows the data of the person. It has a chip next to its title ("*name*'s data"), and its entry in the sidebar has a round mark. A line under the recent chats explains the mark.

| Page | Follows | Server routes that read `user_id` |
|---|---|---|
| Projects, and one project (its overview, chats, agents, connections, triggers, loops and memory) | Yes | `/api/chats/projects` and `/api/chats/projects/{id}` with its sub-routes, except `/api/chats/projects/shared`. The handlers resolve the owner with `projectOwner` (`core/http/endpoints/studio/projects.go`), which uses `auth.CanImpersonate`. |
| Every other page | No | |

A page that does not follow shows a notice: "This page shows your own data, not *name*'s." This includes every Administration page, Chats, Files, Agents, Skills and Models.

Some things on a project page still open your own data: the project's folder in Files, its skills and the chat that **Start chat** creates. These are not project routes.

The Knowledge page (collections) does not follow. The collection routes of a single collection read `user_id`, but the list and the create routes read the caller only, so the page could not show the other person's collections. The page already lets an administrator open the collections of other people from the list of users.

The list of routes and pages is in one file, `core/http/react-ui/src/utils/viewAsRoutes.js`. The request guard, the marks and the notice all read it.

## When the person is gone

If the server answers that the viewed person does not exist (for example, the account was deleted), View as ends and the UI shows a message. The same happens when the server refuses `user_id` for you with `403`, for example when you are no longer an administrator.

A disabled person still exists, so View as keeps working on a disabled account that you opened before it was disabled.

## A safety rail, not a security boundary

Read-only is a rule of the web UI. The browser adds `user_id=<id>` to the routes in the table, and in read-only mode it stops every request that is not a read (`GET`, `HEAD`, `OPTIONS`) to those routes before it leaves the page.

The server does **not** enforce read-only. It accepts a write with `user_id` from any administrator, in any mode, with or without the web UI. An administrator who uses the API, a script or the developer tools of the browser can change the data of any person at any time. Treat View as as protection against a mistake, and not as a limit on what an administrator may do. To limit it, limit who is an administrator.

View as does not tell the viewed person that an administrator acted on the account, except for the server log line `User impersonation` that each request writes.

## What View as does not do

- It does not show the viewed person a record of the activity.
- It does not view another administrator.
- Chats do not follow it.
- It does not make a server-side read-only guarantee.
