<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.parts.image`

ImagePart and related objects.

## `Image`

```python
Image(blob: bytes, filename: str | None)
```

Bases: `object`

Immutable value object representing an image such as a JPEG, PNG, or GIF.

### `blob`

```python
blob: bytes
```

The binary image bytestream of this image.

### `content_type`

```python
content_type() -> str
```

MIME-type of this image, e.g. `"image/jpeg"`.

### `dpi`

```python
dpi() -> tuple[int, int]
```

A (horz_dpi, vert_dpi) 2-tuple specifying the dots-per-inch resolution of this image.

A default value of (72, 72) is used if the dpi is not specified in the image file.

### `ext`

```python
ext() -> str
```

Canonical file extension for this image e.g. `'png'`.

The returned extension is all lowercase and is the canonical extension for the content type
of this image, regardless of what extension may have been used in its filename, if any.

### `filename`

```python
filename: str | None
```

Filename from path used to load this image, if loaded from the filesystem.

`None` if no filename was used in loading, such as when loaded from an in-memory stream.

### `from_blob`

```python
from_blob(blob: bytes, filename: str | None = None) -> Image
```

Return a new `Image` object loaded from the image binary in `blob`.

### `from_file`

```python
from_file(image_file: str | IO[bytes]) -> Image
```

Return a new `Image` object loaded from `image_file`.

`image_file` can be either a path (str) or a file-like object.

### `sha1`

```python
sha1() -> str
```

SHA1 hash digest of the image blob.

### `size`

```python
size() -> tuple[int, int]
```

A (width, height) 2-tuple specifying the dimensions of this image in pixels.

## `ImagePart`

```python
ImagePart(partname: PackURI, content_type: str, package: Package, blob: bytes, filename: str | None = None)
```

Bases: `Part`

An image part.

An image part generally has a partname matching the regex `ppt/media/image[1-9][0-9]*.*`.

### `desc`

```python
desc: str
```

The filename associated with this image.

Either the filename of the original image or a generic name of the form `image.ext` where
`ext` is appropriate to the image file format, e.g. `'jpg'`. An image created using a path
will have that filename; one created with a file-like object will have a generic name.

### `ext`

```python
ext: str
```

File-name extension for this image e.g. `'png'`.

### `image`

```python
image: Image
```

An `Image` object containing the image in this image part.

Note this is a `pptx.image.Image` object, not a PIL Image.

### `new`

```python
new(package: Package, image: Image) -> ImagePart
```

Return new `ImagePart` instance containing `image`.

`image` is an `Image` object.

### `scale`

```python
scale(scaled_cx: int | None, scaled_cy: int | None) -> tuple[int, int]
```

Return scaled image dimensions in EMU based on the combination of parameters supplied.

If `scaled_cx` and `scaled_cy` are both `None`, the native image size is returned. If
neither `scaled_cx` nor `scaled_cy` is `None`, their values are returned unchanged. If a
value is provided for either `scaled_cx` or `scaled_cy` and the other is `None`, the
missing value is calculated such that the image's aspect ratio is preserved.

### `sha1`

```python
sha1() -> str
```

The 40-character SHA1 hash digest for the image binary of this image part.

like: `"1be010ea47803b00e140b852765cdf84f491da47"`.
