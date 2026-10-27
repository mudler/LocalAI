<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.image.gif`

## `Gif`

Bases: `BaseImageHeader`

Image header parser for GIF images.

Note that the GIF format does not support resolution (DPI) information. Both
horizontal and vertical DPI default to 72.

### `content_type`

```python
content_type
```

MIME content type for this image, unconditionally `image/gif` for GIF
images.

### `default_ext`

```python
default_ext
```

Default filename extension, always 'gif' for GIF images.

### `from_stream`

```python
from_stream(stream)
```

Return `Gif` instance having header properties parsed from GIF image in
`stream`.
