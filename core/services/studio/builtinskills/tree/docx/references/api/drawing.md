<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.drawing`

DrawingML-related objects are in this subpackage.

## `Drawing`

```python
Drawing(drawing: CT_Drawing, parent: t.ProvidesStoryPart)
```

Bases: `Parented`

Container for a DrawingML object.

### `has_picture`

```python
has_picture: bool
```

True when `drawing` contains an embedded picture.

A drawing can contain a picture, but it can also contain a chart, SmartArt, or a
drawing canvas. Methods related to a picture, like `.image`, will raise when the drawing
does not contain a picture. Use this value to determine whether image methods will succeed.

This value is `False` when a linked picture is present. This should be relatively rare and
the image would only be retrievable from the filesystem.

Note this does not distinguish between inline and floating images. The presence of either
one will cause this value to be `True`.

### `image`

```python
image: Image
```

An `Image` proxy object for the image in this (picture) drawing.

Raises `ValueError` when this drawing does contains something other than a picture. Use
`.has_picture` to qualify drawing objects before using this property.

### `replace_picture`

```python
replace_picture(image_descriptor: str | IO[bytes]) -> None
```

Swap this drawing's image bytes, keeping display size and position.

Always writes a new image part, so other shapes sharing the old part are unchanged;
the old relationship stays behind. Refuses a drawing with no picture, a linked picture
whose bytes live outside the package, and a protected document.

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.
