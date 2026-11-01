---
name: pdf
description: Use when the user wants to read a PDF file or create a new PDF in their files. Works with the Office add-on tools, with no command runner.
---
# PDF files with the Office tools

Use the tools of the Office add-on: `addon__office__read-pdf` and
`addon__office__create-pdf`. A file is a path in the user's files, such as
`/reports/plan.pdf`.

## Read a PDF

1. Call `read-pdf` with the path. You get the text page by page, up to 50
   pages in one call.
2. For a longer file, call `read-pdf` again with `firstPage` (and
   `lastPage` if you need a range) to continue.
3. A scanned page has no text. Say that the page has no text and stop. Do
   not guess what it says.
4. Answer from what you read, and give the page numbers.

## Create a PDF

1. Use `create-pdf` with `path` and `blocks`. A block is a `heading`
   (`text` and `level` 1 to 3), a `paragraph` (`text`), `bullets` (`items`)
   or a `table` (`header` and `rows`).
2. The text must use Latin-1 characters only. Replace other characters, or
   tell the user which ones cannot be written.
3. If the path is taken, the old file stays and the new one is saved next
   to it.

## Change a PDF

There is no edit tool. To change a PDF, read it, then create a new PDF with
the changed content, and tell the user that the layout of the original is
not kept.

## When the tools cannot do it

The tools do not fill forms, sign, merge, split or keep the layout of an
existing PDF. Say so before you start, and offer what the tools can do.
