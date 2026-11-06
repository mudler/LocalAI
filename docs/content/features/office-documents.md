+++
disableToc = false
title = "Office documents in chats"
weight = 35
url = "/features/office-documents/"
+++

A chat can read, edit and create Word (`.docx`), Excel (`.xlsx`), PowerPoint
(`.pptx`) and PDF files. The files live in your files, so you open and
download the result from **Files**.

There are two ways to get this. Pick one.

| | Simple Office | Sandbox skills |
|---|---|---|
| How it works | Ready-made tools: `read-*`, `edit-*`, `create-*` | The model writes Python and runs it in the Sandbox |
| Needs | The **Simple Office** add-on | The **Sandbox** add-on with an isolated backend |
| Best for | Quick reads, edits and new files | Work the tools cannot do |
| Platforms | Where add-ons run | Linux |

Both supply the skills named `docx`, `xlsx`, `pptx` and `pdf`. LocalAI has no
built-in fallback: with no eligible provider, no document skill is offered.
Switch the provider on in the chat's **Tools** menu. A saved agent must list
the add-on. When both are eligible, an administrator picks one in
**Settings, Tools** (for `xlsx`, for example). Without a pick, the one
installed first serves.

## Try it with Simple Office

1. As an administrator, open **Add-ons**, **Find add-ons**, and choose
   **Install and turn on** for **Simple Office**.
2. Switch the add-on on in the chat's **Tools** menu.
3. Ask: "Create a Word document `/reports/summary.docx` with a title, three
   bullets about our week and a short table."
4. Open **Files** and the `reports` folder. The new file is there.
5. Ask: "Read it and change the second bullet to say we shipped on Friday."
   The model reads the paragraph indexes, then edits the file in place.

## What the tools do

| Tool | What it does |
|---|---|
| `read-docx`, `read-xlsx`, `read-pptx`, `read-pdf` | Return the text: paragraphs with an index, cells row by row, slides with their shapes, pages. |
| `edit-docx`, `edit-xlsx`, `edit-pptx` | Apply a list of changes. All are saved, or none. |
| `create-docx`, `create-pdf` | Make a file from a `markdown` string. |
| `create-xlsx` | Make a single-sheet file from a `csv` string. Use `edit-xlsx` for formulas, numbers and more sheets. |
| `create-pptx` | Make a deck from slides with a title and bullet lines. |

A path is a path inside your files, such as `/reports/plan.docx`. If a
creation path is taken, the old file stays and the new one is saved next to
it.

## Limits

- Markdown for Word and PDF supports headings, paragraphs, lists, bold and
  italic, and simple tables. Raw HTML and images are refused.
- PDF files use a built-in font, so characters outside Latin-1 are refused.
- A new Excel file stores every value as text, so leading zeros and values
  starting with `=` are kept as typed.
- Details and exact limits: [Simple Office]({{% relref "features/simple-office" %}}).

## Security notes

- The tools work only inside the files that the chat may use. A chat whose
  agent works in one folder sees only that folder.
- Nothing is fetched from links, and no macros or shell commands run.
- The add-on is trusted code like any add-on. See
  [Use add-ons]({{% relref "features/using-addons" %}}).
- The Sandbox skills run model-written code, so they need the isolated
  backend. See [Sandbox]({{% relref "features/sandbox" %}}).
