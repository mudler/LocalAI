<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.parts.document`

`DocumentPart` and closely related objects.

## `DocumentPart`

Bases: `StoryPart`

Main document part of a WordprocessingML (WML) package, aka a .docx file.

Acts as broker to other parts such as image, core properties, and style parts. It
also acts as a convenient delegate when a mid-document object needs a service
involving a remote ancestor. The `Parented.part` property inherited by many content
objects provides access to this part object for that purpose.

### `add_footer_part`

```python
add_footer_part()
```

Return (footer_part, rId) pair for newly-created footer part.

### `add_header_part`

```python
add_header_part()
```

Return (header_part, rId) pair for newly-created header part.

### `comments`

```python
comments: Comments
```

`Comments` object providing access to the comments added to this document.

### `core_properties`

```python
core_properties: CoreProperties
```

A `CoreProperties` object providing read/write access to the core properties
of this document.

### `document`

```python
document
```

A `Document` object providing access to the content of this document.

### `drop_header_part`

```python
drop_header_part(rId: str) -> None
```

Remove related header part identified by `rId`.

### `footer_part`

```python
footer_part(rId: str)
```

Return `FooterPart` related by `rId`.

### `get_style`

```python
get_style(style_id: str | None, style_type: WD_STYLE_TYPE) -> BaseStyle
```

Return the style in this document matching `style_id`.

Returns the default style for `style_type` if `style_id` is `None` or does not
match a defined style of `style_type`.

### `get_style_id`

```python
get_style_id(style_or_name, style_type)
```

Return the style_id (`str`) of the style of `style_type` matching
`style_or_name`.

Returns `None` if the style resolves to the default style for `style_type` or if
`style_or_name` is itself `None`. Raises if `style_or_name` is a style of the
wrong type or names a style not present in the document.

### `header_part`

```python
header_part(rId: str)
```

Return `HeaderPart` related by `rId`.

### `inline_shapes`

```python
inline_shapes()
```

The `InlineShapes` instance containing the inline shapes in the document.

### `numbering_part`

```python
numbering_part() -> NumberingPart
```

A `NumberingPart` object providing access to the numbering definitions for this document.

Creates an empty numbering part if one is not present.

### `save`

```python
save(path_or_stream: str | IO[bytes])
```

Save this document to `path_or_stream`, which can be either a path to a
filesystem location (a string) or a file-like object.

### `settings`

```python
settings: Settings
```

A `Settings` object providing access to the settings in the settings part of
this document.

### `styles`

```python
styles
```

A `Styles` object providing access to the styles in the styles part of this
document.
