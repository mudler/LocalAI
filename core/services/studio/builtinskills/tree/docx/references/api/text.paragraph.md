<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.text.paragraph`

Paragraph-related proxy types.

## `Paragraph`

```python
Paragraph(p: CT_P, parent: t.ProvidesStoryPart)
```

Bases: `StoryChild`

Proxy object wrapping a `<w:p>` element.

### `add_run`

```python
add_run(text: str | None = None, style: str | CharacterStyle | None = None) -> Run
```

Append run containing `text` and having character-style `style`.

`text` can contain tab (``\t``) characters, which are converted to the
appropriate XML form for a tab. `text` can also include newline (``\n``) or
carriage return (``\r``) characters, each of which is converted to a line
break. When `text` is `None`, the new run is empty.

### `alignment`

```python
alignment: WD_PARAGRAPH_ALIGNMENT | None
```

A member of the `WdParagraphAlignment` enumeration specifying the
justification setting for this paragraph.

A value of `None` indicates the paragraph has no directly-applied alignment
value and will inherit its alignment value from its style hierarchy. Assigning
`None` to this property removes any directly-applied alignment value.

### `clear`

```python
clear()
```

Return this same paragraph after removing all its content.

Paragraph-level formatting, such as style, is preserved.

### `contains_page_break`

```python
contains_page_break: bool
```

`True` when one or more rendered page-breaks occur in this paragraph.

### `hyperlinks`

```python
hyperlinks: List[Hyperlink]
```

A `Hyperlink` instance for each hyperlink in this paragraph.

### `insert_paragraph_before`

```python
insert_paragraph_before(text: str | None = None, style: str | ParagraphStyle | None = None) -> Paragraph
```

Return a newly created paragraph, inserted directly before this paragraph.

If `text` is supplied, the new paragraph contains that text in a single run. If
`style` is provided, that style is assigned to the new paragraph.

### `iter_inner_content`

```python
iter_inner_content() -> Iterator[Run | Hyperlink]
```

Generate the runs and hyperlinks in this paragraph, in the order they appear.

The content in a paragraph consists of both runs and hyperlinks. This method
allows accessing each of those separately, in document order, for when the
precise position of the hyperlink within the paragraph text is important. Note
that a hyperlink itself contains runs.

### `paragraph_format`

```python
paragraph_format
```

The `ParagraphFormat` object providing access to the formatting properties
for this paragraph, such as line spacing and indentation.

### `rendered_page_breaks`

```python
rendered_page_breaks: List[RenderedPageBreak]
```

All rendered page-breaks in this paragraph.

Most often an empty list, sometimes contains one page-break, but can contain
more than one is rare or contrived cases.

### `runs`

```python
runs: List[Run]
```

Sequence of `Run` instances corresponding to the <w:r> elements in this
paragraph.

### `style`

```python
style: ParagraphStyle | None
```

Read/Write.

`_ParagraphStyle` object representing the style assigned to this paragraph. If
no explicit style is assigned to this paragraph, its value is the default
paragraph style for the document. A paragraph style name can be assigned in lieu
of a paragraph style object. Assigning `None` removes any applied style, making
its effective value the default paragraph style for the document.

### `text`

```python
text: str
```

The textual content of this paragraph.

The text includes the visible-text portion of any hyperlinks in the paragraph.
Tabs and line breaks in the XML are mapped to ``\t`` and ``\n`` characters
respectively.

Assigning text to this property causes all existing paragraph content to be
replaced with a single run containing the assigned text. A ``\t`` character in
the text is mapped to a ``<w:tab/>`` element and each ``\n`` or ``\r``
character is mapped to a line break. Paragraph-level formatting, such as style,
is preserved. All run-level formatting, such as bold or italic, is removed.
