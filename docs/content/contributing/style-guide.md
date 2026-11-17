+++
disableToc = false
title = "Docs style guide"
description = "How to write and check a page of this documentation."
weight = 40
+++

## Write

- Use short sentences. One idea in each sentence. Use the active voice and the present tense.
- Use digits for numbers. Write the product name as "LocalAI".
- Say what the code does. Do not promise features that are not released. Do not write "coming soon" or "roadmap".
- Do not use marketing words, such as "powerful", "seamless" or "unlock". Do not write "any model". Name the models that run, or say "open models". Describe the product with one sentence: "Run open models for chat, images, speech and video on your own machines. The API speaks the OpenAI and Anthropic formats. Your team gets chats, files and documents on top, and the assistant can write and run code." Do not use slogans in body text. Do not use "workspace" or "platform" as a name for a LocalAI feature.
- Do not claim that data stays on the machine. Text can leave it when you use a cloud model, or an add-on that calls a service. Say which.
- Mark an add-on as an add-on, and say when it is in the development channel of the library.
- Use contractions. Do not use em dashes. Do not write "not X but Y", and do not write a sentence about the page ("This page explains..."). Start with what the reader can do.
- Write product names as the interface shows them, for example Knowledge, Connections, Notifications and Administration.
- Do not write internal host names, addresses, local paths or names of people.

## Page template

1. The goal in one or two sentences.
2. What you need.
3. Numbered steps.
4. What you should see.
5. Troubleshooting, as a table of what you see and what to do.
6. Next steps.

A page answers one task. Move a page over 400 lines to Reference, or split it.

For a feature that the latest release does not have yet, set `devbuild = "feature"` in the front matter (`"addon"` for an add-on, `"install"` for the install page). The note comes from `docs/layouts/partials/devbuild-note.html`, which also names the release. Use one line and no hand-written version text on the page.

## Front matter

```toml
+++
title = "Page title"
description = "One sentence."
weight = 10
+++
```

By default the URL follows the file path, so do not set `url` on a new page. Some pages sit in a subfolder (for example `features/chats-and-agents/`) and set `url` to keep the address they had before the move. Leave that `url` in place: removing it changes the published address and breaks links. When you move a page that has no `url`, add its old URL to `aliases` and to `docs/redirects.txt`. Never remove a line from `docs/redirects.txt`.

## Links and images

- Link to other pages with `{{%/* relref "/section/page" */%}}`.
- Give every image alt text. Check that the image shows in the light and the dark variant.
- Images that you link from a page start with `/docs/images/`.
- Do not commit a screenshot that shows personal data.

## Check before you push

```bash
cd docs
hugo --minify --baseURL https://localai.io/docs/
cd ..
.github/ci/check-docs-redirects.sh docs/public docs/redirects.txt
.github/ci/check-docs-links.py docs/public --fragments
```

The pull request build runs the same checks.
