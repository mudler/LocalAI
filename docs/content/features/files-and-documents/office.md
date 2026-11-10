+++
url = "/features/office/"
disableToc = false
title = "Office documents"
description = "Make and read Word, Excel, PowerPoint and PDF files from a chat with the Simple Office or Sandbox add-on."
weight = 20
aliases = ["/features/simple-office/"]
keywords = ["office", "word", "docx", "excel", "xlsx", "powerpoint", "pptx", "pdf", "documents", "simple office"]
+++

A chat can make and edit Word (`.docx`), Excel (`.xlsx`), PowerPoint (`.pptx`) and PDF files. There are two ways. Both are add-ons from the add-on library. Neither is built in. LocalAI has no built-in fallback when none is installed.

| | Simple Office | Sandbox |
|---|---|---|
| What it is | An add-on with document tools | An add-on that runs Python in an isolated runtime |
| Needs a VM or gVisor | No | Yes |
| Makes | `.docx` and `.pdf` from Markdown, `.xlsx` from CSV, `.pptx`, and edits files | Any document that a Python library can write |
| Control over layout | Basic | Full, with the libraries in the guest image |
| Platforms | Linux and macOS | Linux |

Use Simple Office when you want a quick document and your host cannot run a sandbox. Use the [Sandbox]({{% relref "/features/files-and-documents/sandbox" %}}) when you need charts, exact layout or a conversion. In the current development builds, both add-ons are in the development channel of the library.

The two add-ons work on their own. LocalAI does not route between them and does not change the choice for you.

## Simple Office

Install `office` from the add-on library. Its visible name is **Simple Office**. Its internal ID, tool names and settings use `office`. It has 11 tools: read, edit and create for Word, Excel and PowerPoint, and read and create for PDF.

Files are saved in your files at the path that the model gives. If the path is taken, LocalAI saves the new file next to the old one. It does not change the old one.

Example: ask "Write a one-page summary of the notes in /Research as a Word file". The model reads the notes with the file tools and calls `create-docx`. Open the file in **Files**. The preview pane shows the text, but not the formatting.

### Make a Word or PDF file from Markdown

`create-docx` and `create-pdf` take a `path` and a `markdown` string. The optional `title` is metadata. It does not add a heading to the page.

```json
{"path":"/reports/summary.docx","markdown":"# Summary\n\nA **bold** paragraph.\n\n- First\n- Second","title":"Report"}
```

Use a `.pdf` path with `create-pdf`. Both formats support:

- headings and paragraphs
- bullet and numbered lists
- bold and italic text
- simple rectangular tables

Code is literal text. LocalAI does not run it. Links show their label and are not fetched. The tools refuse raw HTML, images and other unsupported parts with an error. They use no network, no file inclusion, no macros, no shell commands and no outside converters.

### Make a one-sheet workbook from CSV

`create-xlsx` takes a `path` and a `csv` string. The optional `sheet` names the sheet and defaults to `Sheet1`. The optional `title` is metadata.

```json
{"path":"/reports/status.xlsx","csv":"Worker,Status\n00123,Healthy\n","sheet":"Status"}
```

- CSV uses commas and standard quoting. Lines end with LF or CRLF. There is no delimiter guessing.
- The tool makes exactly one sheet. The first row is ordinary data.
- Every field is stored as a string. Leading zeros stay, and values that begin with `=`, `+`, `-` or `@` are not turned into formulas.
- Empty cells stay empty.
- To get numeric cells, formulas or more sheets, use `edit-xlsx` after you create the file.

The tool refuses bad quoting, rows of different length, empty content and invalid sheet names.

### Limits

| Item | Limit |
|---|---|
| Blocks in a Word or PDF document | 2000 |
| Items in a list | 100 |
| Table columns | 20 |
| Table body rows | 500 |
| Rows in a created sheet | 5000 |
| Columns in a created sheet | 200 |
| Cells in a created sheet, empty ones included | 100000 |
| JSON argument of a tool call | 1 MiB |

The PDF tool supports Latin-1 characters only. It refuses other characters before it saves a file. A failed call saves no file.

All paths are in your own workspace. Chat folder rules and read-only access still apply. `create-docx`, `create-pdf` and `create-xlsx` no longer accept the old `blocks` and `sheets` arguments. Use `markdown` or `csv`. For the tool contract, see the [add-on contract reference]({{% relref "/addons/contract-reference#simple-office" %}}).

## With the Sandbox

When the Sandbox is installed and isolated commands work, the `docx`, `xlsx`, `pdf` and `pptx` skills give the model Python procedures. You must switch the Sandbox on for the chat, and a saved agent must be granted the add-on. See [Document skills]({{% relref "/features/chats-and-agents/chats#document-skills" %}}). If both add-ons can serve a skill name, the choice in **Settings > Tools** decides. Without a choice, the one installed first serves it.

## Preview

The Files page and the chat show a preview of a document: the text of a Word file, the first 200 rows and 30 columns of each sheet, and the slides of a deck. Formatting is not shown. Download the file to see it in Office.
