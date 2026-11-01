<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.image.png`

## `Png`

Bases: `BaseImageHeader`

Image header parser for PNG images.

### `content_type`

```python
content_type
```

MIME content type for this image, unconditionally `image/png` for PNG
images.

### `default_ext`

```python
default_ext
```

Default filename extension, always 'png' for PNG images.

### `from_stream`

```python
from_stream(stream)
```

Return a `Png` instance having header properties parsed from image in
`stream`.
