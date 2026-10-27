<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.parts.story`

`StoryPart` and related objects.

## `StoryPart`

Bases: `XmlPart`

Base class for story parts.

A story part is one that can contain textual content, such as the document-part and
header or footer parts. These all share content behaviors like `.paragraphs`,
`.add_paragraph()`, `.add_table()` etc.

### `get_or_add_image`

```python
get_or_add_image(image_descriptor: str | IO[bytes]) -> Tuple[str, Image]
```

Return (rId, image) pair for image identified by `image_descriptor`.

`rId` is the str key (often like "rId7") for the relationship between this story
part and the image part, reused if already present, newly created if not.
`image` is an `Image` instance providing access to the properties of the image,
such as dimensions and image type.

### `get_style`

```python
get_style(style_id: str | None, style_type: WD_STYLE_TYPE) -> BaseStyle
```

Return the style in this document matching `style_id`.

Returns the default style for `style_type` if `style_id` is `None` or does not
match a defined style of `style_type`.

### `get_style_id`

```python
get_style_id(style_or_name: BaseStyle | str | None, style_type: WD_STYLE_TYPE) -> str | None
```

Return str style_id for `style_or_name` of `style_type`.

Returns `None` if the style resolves to the default style for `style_type` or if
`style_or_name` is itself `None`. Raises if `style_or_name` is a style of the
wrong type or names a style not present in the document.

### `new_pic_inline`

```python
new_pic_inline(image_descriptor: str | IO[bytes], width: int | Length | None = None, height: int | Length | None = None) -> CT_Inline
```

Return a newly-created `w:inline` element.

The element contains the image specified by `image_descriptor` and is scaled
based on the values of `width` and `height`.

### `next_id`

```python
next_id: int
```

Next available positive integer id value in this story XML document.

The value is determined by incrementing the maximum existing id value. Gaps in
the existing id sequence are not filled. The id attribute value is unique in the
document, without regard to the element type it appears on.
