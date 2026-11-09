+++
disableToc = false
title = "Office documents"
description = "Make and read Word, Excel, PowerPoint and PDF files from a chat."
weight = 20
+++

A chat can make and edit Word (`.docx`), Excel (`.xlsx`), PowerPoint (`.pptx`) and PDF files. There are two ways. Both are add-ons from the library. Neither is built in, and LocalAI has no built-in fallback when none is installed.

| | Simple Office | Sandbox |
|---|---|---|
| What it is | An add-on with document tools | An add-on that runs Python in an isolated runtime |
| Needs a VM or gVisor | No | Yes |
| Makes | `.docx` and `.pdf` from Markdown, `.xlsx` from CSV, `.pptx`, and edits files | Any document that a Python library can write |
| Control over layout | Basic | Full, with the libraries in the guest image |
| Platforms | Linux and macOS | Linux |

Use Simple Office when you want a quick document and your host cannot run a sandbox. Use the [Sandbox]({{% relref "/features/sandbox" %}}) when you need charts, exact layout or a conversion. In the current development builds, both add-ons are in the development channel of the library.

## Simple Office

Install **Simple Office** from the add-on library. It has 11 tools: read, edit and create for Word, Excel and PowerPoint, and read and create for PDF. Files are saved in your files at the path that the model gives. If the path is taken, the new file is saved next to the old one, and the old one is not changed.

Limits: 2000 blocks in a document, 100 items in a list, 20 columns and 500 rows in a table. A created sheet has at most 5000 rows and 200 columns. The PDF tool supports Latin-1 characters only and refuses others before it saves. Raw HTML and images in the Markdown are refused. For the full list, see [Simple Office]({{% relref "/features/simple-office" %}}).

Example: ask "Write a one-page summary of the notes in /Research as a Word file". The model reads the notes with the file tools and calls `create-docx`. Open the file in **Files**. The preview pane shows the text, but not the formatting.

## With the Sandbox

When the Sandbox is installed and isolated commands work, the `docx`, `xlsx`, `pdf` and `pptx` skills give the model Python procedures. You must switch the Sandbox on for the chat, and a saved agent must be granted the add-on. See [Document skills]({{% relref "/features/chats#document-skills" %}}). If both add-ons can serve a skill name, the choice in **Settings > Tools** decides. Without a choice, the one installed first serves it.

## Preview

The Files page and the chat show a preview of a document: the text of a Word file, the first 200 rows and 30 columns of each sheet, and the slides of a deck. Formatting is not shown. Download the file to see it in Office.
