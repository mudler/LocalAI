+++
url = "/features/office/"
disableToc = false
title = "Office documents"
description = "Make and read Word, Excel, PowerPoint and PDF files from a chat with the Simple Office or Sandbox add-on."
weight = 20
aliases = ["/features/simple-office/"]
keywords = ["office", "word", "docx", "excel", "xlsx", "powerpoint", "pptx", "pdf", "documents", "simple office"]
devbuild = "addon"
+++

A chat can make and edit Word (`.docx`), Excel (`.xlsx`), PowerPoint (`.pptx`) and PDF files. Two add-ons can do this: Simple Office and Sandbox. Install one from the add-on library. Without one of them, a chat can't make these files.

Use Simple Office when you want a quick document and your host can't run a sandbox. Use the [Sandbox]({{% relref "/addons/catalogue/work/sandbox" %}}) when you need charts, exact layout or a conversion.

The two add-ons work on their own. LocalAI doesn't pick one for you. For the document skills, see [With the Sandbox](#with-the-sandbox).

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

Simple Office only writes text, lists and tables. It runs no code, fetches no links and refuses raw HTML and images with an error.

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

### Make a deck in the design of a template

`create-pptx` takes a `path` and `slides`. Each slide has a `title` and optional `bullets`. A slide without bullets is a title slide. Without more, the deck uses the built-in design.

Give the optional `template` to build the deck on the design of a `.pptx` or `.potx` file in your files:

```json
{"path":"/reports/plan.pptx","template":"/templates/brand.potx","title":"Plan","slides":[{"title":"Plan 2027"},{"title":"Goals","bullets":["Ship","Measure"]}]}
```

- The deck keeps the template's slide masters, layouts, theme colors, theme fonts and slide size.
- Slides with bullets use the first layout of type `obj` (Title and Content), else `tx`. Slides without bullets use the layout of type `title` (Title Slide), or Title and Content when there is none. Layouts are found by type, not by name, so a template saved in any language works.
- The slides in the template are not copied. The deck holds only the slides you give.
- The template is never changed. Its `title` metadata is replaced by the deck's `title`.
- A template without a Title and Content layout, or a file that is not a PowerPoint deck or template, is refused, and no file is saved. The template path follows the same rules as every other path.

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

All paths are in your own files. Chat folder rules and read-only access still apply. A path with the wrong extension is refused with a message that quotes the path as received, so the model can see what it sent. A path wrapped in quotes, such as `"/plan.pptx"` with the quotes, also gets the hint to remove them. The quotes are not stripped, because a file name may contain quotes.

`create-docx` and `create-pdf` take `markdown`. `create-xlsx` takes `csv`.

For the tool contract, see the [add-on contract reference]({{% relref "/addons/contract-reference/first-party-packages#simple-office" %}}).

## With the Sandbox

When the Sandbox is installed and isolated commands work, the `docx`, `xlsx`, `pdf` and `pptx` skills give the model Python procedures. You must switch the Sandbox on for the chat, and a saved agent must be granted the add-on. See [Document skills]({{% relref "/features/chats-and-agents/chats-skills#document-skills" %}}). If both add-ons can serve a skill name, the choice in **Settings > Tools** decides. Without a choice, the one installed first serves it.

## Which one?

| | Simple Office | Sandbox |
|---|---|---|
| What it is | An add-on with document tools | An add-on that runs Python in an isolated runtime |
| Makes | `.docx` and `.pdf` from Markdown, `.xlsx` from CSV, `.pptx`, and edits files | Any document that a Python library can write |
| Control over layout | Basic | Full, with the libraries in the guest image |
| Platform | Linux | See the [Sandbox]({{% relref "/addons/catalogue/work/sandbox" %}}) page |

## Preview

The Files page and the chat show a preview of a document: the text of a Word file, the first 200 rows and 30 columns of each sheet, and the slides of a deck. Formatting is not shown. Download the file to see it in Office.
