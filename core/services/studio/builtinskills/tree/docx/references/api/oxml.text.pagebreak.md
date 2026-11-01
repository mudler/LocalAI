<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.text.pagebreak`

Custom element class for rendered page-break (CT_LastRenderedPageBreak).

## `CT_LastRenderedPageBreak`

Bases: `BaseOxmlElement`

`<w:lastRenderedPageBreak>` element, indicating page break inserted by renderer.

A rendered page-break is one inserted by the renderer when it runs out of room on a
page. It is an empty element (no attrs or children) and is a child of CT_R, peer to
CT_Text.

NOTE: this complex-type name does not exist in the schema, where
`w:lastRenderedPageBreak` maps to `CT_Empty`. This name was added to give it
distinguished behavior. CT_Empty is used for many elements.

### `following_fragment_p`

```python
following_fragment_p: CT_P
```

A "loose" `CT_P` containing only the paragraph content before this break.

Raises `ValueError` if this `w:lastRenderedPageBreak` is not the first rendered
page-break in its paragraph.

The returned `CT_P` is a "clone" (deepcopy) of the `w:p` ancestor of this
page-break with this `w:lastRenderedPageBreak` element and all content preceding
it removed.

NOTE: this `w:p` can itself contain one or more `w:renderedPageBreak` elements
(when the paragraph contained more than one). While this is rare, the caller
should treat this paragraph the same as other paragraphs and split it if
necessary in a folloing step or recursion.

### `follows_all_content`

```python
follows_all_content: bool
```

True when this page-break element is the last "content" in the paragraph.

This is very uncommon case and may only occur in contrived or cases where the
XML is edited by hand, but it is not precluded by the spec.

### `precedes_all_content`

```python
precedes_all_content: bool
```

True when a `w:lastRenderedPageBreak` precedes all paragraph content.

This is a common case; it occurs whenever the page breaks on an even paragraph
boundary.

### `preceding_fragment_p`

```python
preceding_fragment_p: CT_P
```

A "loose" `CT_P` containing only the paragraph content before this break.

Raises `ValueError` if this `w:lastRenderedPageBreak` is not the first rendered
paragraph in its paragraph.

The returned `CT_P` is a "clone" (deepcopy) of the `w:p` ancestor of this
page-break with this `w:lastRenderedPageBreak` element and all its following
siblings removed.
