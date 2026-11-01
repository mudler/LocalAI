---
name: pptx
description: Use when the user wants to read, change or create a PowerPoint deck (.pptx) in their files. Works with the Office add-on tools, with no command runner.
---
# PowerPoint decks with the Office tools

Use the tools of the Office add-on: `addon__office__read-pptx`,
`addon__office__edit-pptx` and `addon__office__create-pptx`. A file is a
path in the user's files, such as `/reports/plan.pptx`.

## Read first

1. Call `read-pptx` with the path. You get each slide with its title and
   its text shapes: the shape `id`, its kind and its paragraphs.
2. Answer from what you read. Do not guess text you did not read.

## Change a deck

1. Read the deck first to get the slide numbers and the shape ids.
2. Call `edit-pptx` with `path` and `edits`. Each edit has an `op`:
   - `title`: set the title of `slide` to `text`.
   - `text`: set paragraph `paragraph` (from 0) of shape `shape` on `slide`
     to `text`.
   - `add`: add a slide with `text` as its title and `bullets`. `layout` is
     optional; the default is Title and Content.
   - `delete`: remove `slide`.
3. Slides count from 1. An add or a delete moves the slide numbers after
   it: plan the order, or read the deck again.
4. Change only the text that must change. Do not create the deck again to
   fix one slide.

## Create a deck

Use `create-pptx` with `path` and `slides`, each with a `title` and its
`bullets`. A slide without bullets is a title slide. Build the outline first,
one slide per point. If the path is taken, the old file stays and the new one
is saved next to it.

## When the tools cannot do it

The tools change text only. They do not move shapes, change the design,
add images, charts, animations or speaker notes. Say so before you promise
it, and offer what the tools can do.
