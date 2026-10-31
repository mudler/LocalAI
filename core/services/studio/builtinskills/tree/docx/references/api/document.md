<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.document`

`Document` and closely related objects.

## `Document`

```python
Document(element: CT_Document, part: DocumentPart)
```

Bases: `ElementProxy`

WordprocessingML (WML) document.

Not intended to be constructed directly. Use `docx.Document` to open or create
a document.

### `add_comment`

```python
add_comment(runs: Run | Sequence[Run], text: str | None = '', author: str = '', initials: str | None = '') -> Comment
```

Add a comment anchored to `runs`, and return it.

Pass one `Run` or a sequence; only the first and last are used, so handing over
`paragraph.runs` works. Reach for `Span.comment` instead when the anchor should match
exact text rather than whole runs. Refuses a protected document, runs belonging to
another document, and a comments part that is missing or ambiguous.

### `add_heading`

```python
add_heading(text: str = '', level: int = 1)
```

Return a heading paragraph newly added to the end of the document.

The heading paragraph will contain `text` and have its paragraph style
determined by `level`. If `level` is 0, the style is set to `Title`. If `level`
is 1 (or omitted), `Heading 1` is used. Otherwise the style is set to `Heading
{level}`. Raises `ValueError` if `level` is outside the range 0-9.

### `add_page_break`

```python
add_page_break()
```

Return newly `Paragraph` object containing only a page break.

### `add_paragraph`

```python
add_paragraph(text: str = '', style: str | ParagraphStyle | None = None) -> Paragraph
```

Return paragraph newly added to the end of the document.

The paragraph is populated with `text` and having paragraph style `style`.

`text` can contain tab (``\t``) characters, which are converted to the
appropriate XML form for a tab. `text` can also include newline (``\n``) or
carriage return (``\r``) characters, each of which is converted to a line
break.

### `add_picture`

```python
add_picture(image_path_or_stream: str | IO[bytes], width: int | Length | None = None, height: int | Length | None = None)
```

Return new picture shape added in its own paragraph at end of the document.

The picture contains the image at `image_path_or_stream`, scaled based on
`width` and `height`. If neither width nor height is specified, the picture
appears at its native size. If only one is specified, it is used to compute a
scaling factor that is then applied to the unspecified dimension, preserving the
aspect ratio of the image. The native size of the picture is calculated using
the dots-per-inch (dpi) value specified in the image file, defaulting to 72 dpi
if no value is specified, as is often the case.

### `add_section`

```python
add_section(start_type: WD_SECTION = WD_SECTION.NEW_PAGE)
```

Return a `Section` object newly added at the end of the document.

The optional `start_type` argument must be a member of the `WdSectionStart`
enumeration, and defaults to ``WD_SECTION.NEW_PAGE`` if not provided.

### `add_table`

```python
add_table(rows: int, cols: int, style: str | _TableStyle | None = None)
```

Add a table having row and column counts of `rows` and `cols` respectively.

`style` may be a table style object or a table style name. If `style` is `None`,
the table inherits the default table style of the document.

### `comments`

```python
comments: Comments
```

A `Comments` object providing access to comments added to the document.

### `core_properties`

```python
core_properties
```

A `CoreProperties` object providing Dublin Core properties of document.

### `inline_shapes`

```python
inline_shapes
```

The `InlineShapes` collection for this document.

An inline shape is a graphical object, such as a picture, contained in a run of
text and behaving like a character glyph, being flowed like other text in a
paragraph.

### `iter_inner_content`

```python
iter_inner_content() -> Iterator[Paragraph | Table]
```

Generate each `Paragraph` or `Table` in this document in document order.

### `paragraphs`

```python
paragraphs: List[Paragraph]
```

The `Paragraph` instances in the document, in document order.

Note that paragraphs within revision marks such as ``<w:ins>`` or ``<w:del>`` do
not appear in this list.

### `part`

```python
part: DocumentPart
```

The `DocumentPart` object of this document.

### `revisions`

```python
revisions
```

`Revisions` object enumerating tracked changes across all story parts.

paper-docx addition: a fresh snapshot on each access; supports
accept/reject of all revisions or filtered by author. See
`docx.revision`.

### `save`

```python
save(path_or_stream: str | IO[bytes])
```

Save this document to `path_or_stream`.

`path_or_stream` can be either a path to a filesystem location (a string) or a
file-like object.

### `sections`

```python
sections: Sections
```

`Sections` object providing access to each section in this document.

### `settings`

```python
settings: Settings
```

A `Settings` object providing access to the document-level settings.

### `styles`

```python
styles
```

A `Styles` object providing access to the styles in this document.

### `tables`

```python
tables: List[Table]
```

All `Table` instances in the document, in document order.

Note that only tables appearing at the top level of the document appear in this
list; a table nested inside a table cell does not appear. A table within
revision marks such as ``<w:ins>`` or ``<w:del>`` will also not appear in the
list.

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.
