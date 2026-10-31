<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.image.image`

Provides objects that can characterize image streams.

## `BaseImageHeader`

```python
BaseImageHeader(px_width: int, px_height: int, horz_dpi: int, vert_dpi: int)
```

Base class for image header subclasses like `Jpeg` and `Tiff`.

### `content_type`

```python
content_type: str
```

Abstract property definition, must be implemented by all subclasses.

### `default_ext`

```python
default_ext: str
```

Default filename extension for images of this type.

An abstract property definition, must be implemented by all subclasses.

### `horz_dpi`

```python
horz_dpi
```

Integer dots per inch for the width of this image.

Defaults to 72 when not present in the file, as is often the case.

### `px_height`

```python
px_height
```

The vertical pixel dimension of the image.

### `px_width`

```python
px_width
```

The horizontal pixel dimension of the image.

### `vert_dpi`

```python
vert_dpi
```

Integer dots per inch for the height of this image.

Defaults to 72 when not present in the file, as is often the case.

## `Image`

```python
Image(blob: bytes, filename: str, image_header: BaseImageHeader)
```

Graphical image stream such as JPEG, PNG, or GIF with properties and methods
required by ImagePart.

### `blob`

```python
blob
```

The bytes of the image 'file'.

### `content_type`

```python
content_type: str
```

MIME content type for this image, e.g. ``'image/jpeg'`` for a JPEG image.

### `ext`

```python
ext()
```

The file extension for the image.

If an actual one is available from a load filename it is used. Otherwise a
canonical extension is assigned based on the content type. Does not contain the
leading period, e.g. 'jpg', not '.jpg'.

### `filename`

```python
filename
```

Original image file name, if loaded from disk, or a generic filename if
loaded from an anonymous stream.

### `from_blob`

```python
from_blob(blob: bytes) -> Image
```

Return a new `Image` subclass instance parsed from the image binary contained
in `blob`.

### `from_file`

```python
from_file(image_descriptor: str | IO[bytes])
```

Return a new `Image` subclass instance loaded from the image file identified
by `image_descriptor`, a path or file-like object.

### `height`

```python
height: Inches
```

A `Length` value representing the native height of the image, calculated from
the values of `px_height` and `vert_dpi`.

### `horz_dpi`

```python
horz_dpi: int
```

Integer dots per inch for the width of this image.

Defaults to 72 when not present in the file, as is often the case.

### `px_height`

```python
px_height: int
```

The vertical pixel dimension of the image.

### `px_width`

```python
px_width: int
```

The horizontal pixel dimension of the image.

### `scaled_dimensions`

```python
scaled_dimensions(width: int | Length | None = None, height: int | Length | None = None) -> Tuple[Length, Length]
```

(cx, cy) pair representing scaled dimensions of this image.

The native dimensions of the image are scaled by applying the following rules to
the `width` and `height` arguments.

* If both `width` and `height` are specified, the return value is (`width`,
`height`); no scaling is performed.
* If only one is specified, it is used to compute a scaling factor that is then
applied to the unspecified dimension, preserving the aspect ratio of the image.
* If both `width` and `height` are `None`, the native dimensions are returned.

The native dimensions are calculated using the dots-per-inch (dpi) value
embedded in the image, defaulting to 72 dpi if no value is specified, as is
often the case. The returned values are both `Length` objects.

### `sha1`

```python
sha1()
```

SHA1 hash digest of the image blob.

### `vert_dpi`

```python
vert_dpi: int
```

Integer dots per inch for the height of this image.

Defaults to 72 when not present in the file, as is often the case.

### `width`

```python
width: Inches
```

A `Length` value representing the native width of the image, calculated from
the values of `px_width` and `horz_dpi`.
