<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.parts.endnotes`

`EndnotesPart`, the story part containing a document's endnotes.

## `EndnotesPart`

Bases: `StoryPart`

Proxy for the endnotes part (`word/endnotes.xml`) of a document.

Registered for its content type so endnote content loads as a live XML
part (visible to `docx.story` traversal) instead of an opaque blob.
paper-docx v0 reads endnotes; it does not create the part.
