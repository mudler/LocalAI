<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.text.pagebreak`

Proxy objects related to rendered page-breaks.

## `RenderedPageBreak`

```python
RenderedPageBreak(lastRenderedPageBreak: CT_LastRenderedPageBreak, parent: t.ProvidesStoryPart)
```

Bases: `Parented`

A page-break inserted by Word during page-layout for print or display purposes.

This usually does not correspond to a "hard" page-break inserted by the document
author, rather just that Word ran out of room on one page and needed to start
another. The position of these can change depending on the printer and page-size, as
well as margins, etc. They also will change in response to edits, but not until Word
loads and saves the document.

Note these are never inserted by `python-docx` because it has no rendering function.
These are generally only useful for text-extraction of existing documents when
`python-docx` is being used solely as a document "reader".

NOTE: a rendered page-break can occur within a hyperlink; consider a multi-word
hyperlink like "excellent Wikipedia article on LLMs" that happens to fall close to
the end of the last line on a page such that the page breaks between "Wikipedia" and
"article". In such a "page-breaks-in-hyperlink" case, THESE METHODS WILL "MOVE" THE
PAGE-BREAK to occur after the hyperlink, such that the entire hyperlink appears in
the paragraph returned by `.preceding_paragraph_fragment`. While this places the
"tail" text of the hyperlink on the "wrong" page, it avoids having two hyperlinks
each with a fragment of the actual text and pointing to the same address.

### `following_paragraph_fragment`

```python
following_paragraph_fragment: Paragraph | None
```

A "loose" paragraph containing the content following this page-break.

HAS POTENTIALLY SURPRISING BEHAVIORS so read carefully to be sure this is what
you want. This is primarily targeted toward text-extraction use-cases for which
precisely associating text with the page it occurs on is important.

Compare `.preceding_paragraph_fragment` as these two are intended to be used
together.

This value is `None` when no content follows this page-break. This case is
unlikely to occur in practice because Word places even-paragraph-boundary
page-breaks on the paragraph *following* the page-break. Still, it is possible
and must be checked for. Returning `None` for this case avoids "inserting" an
extra, non-existent paragraph into the content stream. Note that content can
include DrawingML items like images or charts, not just text.

The returned paragraph *is divorced from the document body*. Any changes made to
it will not be reflected in the document. It is intended to provide a container
(`Paragraph`) with familiar properties and methods that can be used to
characterize the paragraph content following a mid-paragraph page-break.

Contains no portion of the hyperlink when this break occurs within a hyperlink.

### `preceding_paragraph_fragment`

```python
preceding_paragraph_fragment: Paragraph | None
```

A "loose" paragraph containing the content preceding this page-break.

Compare `.following_paragraph_fragment` as these two are intended to be used
together.

This value is `None` when no content precedes this page-break. This case is
common and occurs whenever a page breaks on an even paragraph boundary.
Returning `None` for this case avoids "inserting" a non-existent paragraph into
the content stream. Note that content can include DrawingML items like images or
charts.

Note the returned paragraph *is divorced from the document body*. Any changes
made to it will not be reflected in the document. It is intended to provide a
familiar container (`Paragraph`) to interrogate for the content preceding this
page-break in the paragraph in which it occured.

Contains the entire hyperlink when this break occurs within a hyperlink.
