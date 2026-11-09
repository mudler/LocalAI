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
- Do not use marketing words, such as "powerful", "seamless" or "unlock". Do not write "any model". Say "run open models on your own hardware".
- Do not claim that data stays on the machine. Text can leave it when you use a cloud model, or an add-on that calls a service. Say which.
- Mark an add-on as an add-on, and say when it is in the development channel of the library.
- Do not write internal host names, addresses, local paths or names of people.

## Page template

1. The goal in one or two sentences.
2. What you need.
3. Numbered steps.
4. What you should see.
5. Troubleshooting, as a table of what you see and what to do.
6. Next steps.

A page answers one task. Move a page over 400 lines to Reference, or split it.

## Front matter

```toml
+++
title = "Page title"
description = "One sentence."
weight = 10
+++
```

The URL follows the file path. Do not set `url`. When you move a page, add its old URL to `aliases` and to `docs/redirects.txt`.

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
