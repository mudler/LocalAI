---
title: "Simple Office"
weight: 35
---

Simple Office is a lightweight add-on for basic Word, Excel, PowerPoint and
PDF file work in chats, without requiring Sandbox. Install `office` from the
add-on library. Its internal ID, tool names, settings and resource references
remain `office`; the visible name is **Simple Office**. Sandbox workflows remain
independent. There is no automatic fallback, routing or provider preference change.

## Create documents with Markdown

`create-docx` and `create-pdf` require string fields `path` and `markdown`.
Optional `title` is string metadata only; it does not add a visible heading.

```json
{"path":"/reports/summary.docx","markdown":"# Summary\n\nA **bold** paragraph.\n\n- First\n- Second","title":"Report"}
```

Use a `.pdf` path with `create-pdf`. Both formats support headings, paragraphs,
bullet and numbered lists, bold/italic emphasis, and simple rectangular tables.
Inline and fenced code are literal text, never executed. Links show visible
labels without fetching their targets. Raw HTML, images and unsupported
constructs are rejected with an error. No network fetching, file inclusion,
macros, shell commands or external converters are used.

Limits are 2000 document blocks, 100 items per list, 20 table columns and
500 body rows per table. The full JSON argument budget is 1 MiB; parsing is
bounded internally. PDF retains its built-in-font Latin-1 restrictions;
unsupported characters are refused before saving. No fonts or Unicode support
are added by this change.

## Create a single-sheet workbook with CSV

`create-xlsx` requires string fields `path` and `csv`; optional string `sheet`
defaults to `Sheet1`, and optional string `title` is workbook metadata.

```json
{"path":"/reports/status.xlsx","csv":"Worker,Status\n00123,Healthy\n","sheet":"Status"}
```

CSV uses commas and standard quoting, including escaped quotes and multiline
quoted cells, with LF or CRLF line endings. There is no delimiter guessing.
Exactly one sheet is created. The first row is ordinary data, with no implicit
header styling. Every nonempty field is stored as a string: leading zeros and
values beginning with `=`, `+`, `-` or `@` are preserved, not inferred as numbers
or formulas. Empty cells stay blank at their original coordinates. Use
`edit-xlsx` afterwards for explicit numeric cells, formulas or additional sheets;
its existing behavior has not changed.

Malformed quoting, ragged rows, only-empty content and invalid sheet names are
refused. Limits are 5000 rows, 200 columns and 100000 grid cells, including empty
fields, within the full 1 MiB JSON argument budget.

## Compatibility change

The three creation tools no longer accept the old `blocks` (DOCX/PDF) or
`sheets` (XLSX) structures. Update callers to `markdown` or `csv` respectively.
All advertised arguments of these three tools are strings; there is no alternate
structured-array creation contract. Read/edit tools and `create-pptx` are unchanged.
Multiple-sheet creation is not supported by `create-xlsx`.

All paths are inside the caller's authorized workspace, not arbitrary host paths
or content URLs. Chat folder and read-only access rules still apply. Failed
parsing or rendering saves no new file. Existing files are never overwritten by
creation: a conflict saves the new result to a sibling path and reports it.
Cancellation, authorization, extension checks and atomic workspace storage remain
in force. These simplified catalog schemas are not evidence of live model tool
selection or of general native grammar-parser correctness.
