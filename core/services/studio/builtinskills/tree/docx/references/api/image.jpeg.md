<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.image.jpeg`

Objects related to parsing headers of JPEG image streams.

## `Exif`

Bases: `Jpeg`

Image header parser for Exif image format.

### `from_stream`

```python
from_stream(stream)
```

Return `Exif` instance having header properties parsed from Exif image in
`stream`.

## `Jfif`

Bases: `Jpeg`

Image header parser for JFIF image format.

### `from_stream`

```python
from_stream(stream)
```

Return a `Jfif` instance having header properties parsed from image in
`stream`.

## `Jpeg`

Bases: `BaseImageHeader`

Base class for JFIF and EXIF subclasses.

### `content_type`

```python
content_type
```

MIME content type for this image, unconditionally `image/jpeg` for JPEG
images.

### `default_ext`

```python
default_ext
```

Default filename extension, always 'jpg' for JPG images.
