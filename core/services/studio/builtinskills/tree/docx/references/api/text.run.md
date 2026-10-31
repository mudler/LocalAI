<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.text.run`

Run-related proxy objects for python-docx, Run in particular.

## `Run`

```python
Run(r: CT_R, parent: t.ProvidesStoryPart)
```

Bases: `StoryChild`

Proxy object wrapping `<w:r>` element.

Several of the properties on Run take a tri-state value, `True`, `False`, or `None`.
`True` and `False` correspond to on and off respectively. `None` indicates the
property is not specified directly on the run and its effective value is taken from
the style hierarchy.

### `add_break`

```python
add_break(break_type: WD_BREAK = WD_BREAK.LINE)
```

Add a break element of `break_type` to this run.

`break_type` can take the values `WD_BREAK.LINE`, `WD_BREAK.PAGE`, and
`WD_BREAK.COLUMN` where `WD_BREAK` is imported from `docx.enum.text`.
`break_type` defaults to `WD_BREAK.LINE`.

### `add_picture`

```python
add_picture(image_path_or_stream: str | IO[bytes], width: int | Length | None = None, height: int | Length | None = None) -> InlineShape
```

Return `InlineShape` containing image identified by `image_path_or_stream`.

The picture is added to the end of this run.

`image_path_or_stream` can be a path (a string) or a file-like object containing
a binary image.

If neither width nor height is specified, the picture appears at
its native size. If only one is specified, it is used to compute a scaling
factor that is then applied to the unspecified dimension, preserving the aspect
ratio of the image. The native size of the picture is calculated using the dots-
per-inch (dpi) value specified in the image file, defaulting to 72 dpi if no
value is specified, as is often the case.

### `add_tab`

```python
add_tab() -> None
```

Add a ``<w:tab/>`` element at the end of the run, which Word interprets as a
tab character.

### `add_text`

```python
add_text(text: str)
```

Returns a newly appended `_Text` object (corresponding to a new ``<w:t>``
child element) to the run, containing `text`.

Compare with the possibly more friendly approach of assigning text to the
`Run.text` property.

### `bold`

```python
bold: bool | None
```

Read/write tri-state value.

When `True`, causes the text of the run to appear in bold face. When `False`,
the text unconditionally appears non-bold. When `None` the bold setting for this
run is inherited from the style hierarchy.

### `clear`

```python
clear()
```

Return reference to this run after removing all its content.

All run formatting is preserved.

### `contains_page_break`

```python
contains_page_break: bool
```

`True` when one or more rendered page-breaks occur in this run.

Note that "hard" page-breaks inserted by the author are not included. A hard
page-break gives rise to a rendered page-break in the right position so if those
were included that page-break would be "double-counted".

It would be very rare for multiple rendered page-breaks to occur in a single
run, but it is possible.

### `element`

```python
element = r
```

### `font`

```python
font: Font
```

The `Font` object providing access to the character formatting properties for
this run, such as font name and size.

### `italic`

```python
italic: bool | None
```

Read/write tri-state value.

When `True`, causes the text of the run to appear in italics. When `False`, the
text unconditionally appears non-italic. When `None` the italic setting for this
run is inherited from the style hierarchy.

### `iter_inner_content`

```python
iter_inner_content() -> Iterator[str | Drawing | RenderedPageBreak]
```

Generate the content-items in this run in the order they appear.

NOTE: only content-types currently supported by `python-docx` are generated. In
this version, that is text and rendered page-breaks. Drawing is included but
currently only provides access to its XML element (CT_Drawing) on its
`._drawing` attribute. `Drawing` attributes and methods may be expanded in
future releases.

There are a number of element-types that can appear inside a run, but most of
those (w:br, w:cr, w:noBreakHyphen, w:t, w:tab) have a clear plain-text
equivalent. Any contiguous range of such elements is generated as a single
`str`. Rendered page-break and drawing elements are generated individually. Any
other elements are ignored.

### `mark_comment_range`

```python
mark_comment_range(last_run: Run, comment_id: int) -> None
```

Mark the runs from this one to `last_run` as a comment range for `comment_id`.

Called by the comment-anchoring machinery; prefer `Document.add_comment` or
`Span.comment`. Refuses when the two runs belong to different documents, leaving the
XML unchanged.

### `style`

```python
style: CharacterStyle
```

Read/write.

A `CharacterStyle` object representing the character style applied to this run.
The default character style for the document (often `Default Character Font`) is
returned if the run has no directly-applied character style. Setting this
property to `None` removes any directly-applied character style.

### `text`

```python
text: str
```

String formed by concatenating the text equivalent of each run.

Each `<w:t>` element adds the text characters it contains. A `<w:tab/>` element
adds a `\t` character. A `<w:cr/>` or `<w:br>` element each add a `\n`
character. Note that a `<w:br>` element can indicate a page break or column
break as well as a line break. Only line-break `<w:br>` elements translate to
a `\n` character. Others are ignored. All other content child elements, such as
`<w:drawing>`, are ignored.

Assigning text to this property has the reverse effect, translating each `\t`
character to a `<w:tab/>` element and each `\n` or `\r` character to a
`<w:cr/>` element. Any existing run content is replaced. Run formatting is
preserved.

### `underline`

```python
underline: bool | WD_UNDERLINE | None
```

The underline style for this `Run`.

Value is one of `None`, `True`, `False`, or a member of `WdUnderline`.

A value of `None` indicates the run has no directly-applied underline value and
so will inherit the underline value of its containing paragraph. Assigning
`None` to this property removes any directly-applied underline value.

A value of `False` indicates a directly-applied setting of no underline,
overriding any inherited value.

A value of `True` indicates single underline.

The values from `WdUnderline` are used to specify other outline styles such
as double, wavy, and dotted.

## `rollback_xml_on_error`

```python
rollback_xml_on_error(root: '_Element') -> Generator[None, None, None]
```

Restore one live XML tree after an error in a local compound edit.
