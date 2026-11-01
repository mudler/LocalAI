<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.parts.footnotes`

`FootnotesPart`, the story part containing a document's footnotes.

## `FootnotesPart`

Bases: `StoryPart`

Proxy for the footnotes part (`word/footnotes.xml`) of a document.

Registered for its content type so footnote content loads as a live XML
part (visible to `docx.story` traversal) instead of an opaque blob.
paper-docx v0 reads footnotes; it does not create the part.
