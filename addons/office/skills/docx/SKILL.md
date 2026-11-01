---
name: docx
description: Use when the user wants to read, change or create a Word document (.docx) in their files. Works with the Office add-on tools, with no command runner.
---
# Word documents with the Office tools

Use the tools of the Office add-on: `addon__office__read-docx`,
`addon__office__edit-docx` and `addon__office__create-docx`. A file is a
path in the user's files, such as `/reports/plan.docx`.

## Read first

1. Call `read-docx` with the path. You get the paragraphs with their index,
   and the tables.
2. Answer from what you read. Do not guess text you did not read.

## Change a document

1. Read the document first to get the paragraph indexes.
2. Call `edit-docx` with `path` and `edits`. Each edit has an `op`:
   - `set`: replace the text of the paragraph `index`.
   - `insert`: add a paragraph with `text` after the paragraph `after`
     (`-1` for the start). `style` is optional and must be a style of the
     document.
   - `delete`: remove the paragraph `index`.
   - `replace`: replace `find` with `text` everywhere.
3. The edits run in order, and all or none are saved. An insert or a delete
   moves the indexes after it: plan the order, or read the document again.
4. Change the smallest range of paragraphs. Do not create the document
   again to fix a typo.
5. If someone changed the file meanwhile, your version is saved as a second
   file. Tell the user its path.

## Create a document

Use `create-docx` with `path` and `blocks`. A block is a `heading` (`text`
and `level` 1 to 3), a `paragraph` (`text`) or `bullets` (`items`). If the
path is taken, the old file stays and the new one is saved next to it.

## When the tools cannot do it

The tools do not edit tables, tracked changes, comments, images, headers,
footers or fields. Say so before you start, and offer what the tools can do.
Do not describe a manual method instead.
