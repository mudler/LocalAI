<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shapes.picture`

Shapes based on the `p:pic` element, including Picture and Movie.

## `Movie`

Bases: `_BasePicture`

A movie shape, one that places a video on a slide.

Like `Picture`, a movie shape is based on the `p:pic` element. A movie is composed of a video
and a *poster frame*, the placeholder image that represents the video before it is played.

### `media_format`

```python
media_format() -> _MediaFormat
```

The `_MediaFormat` object for this movie.

The `_MediaFormat` object provides access to formatting properties for the movie.

### `media_type`

```python
media_type: PP_MEDIA_TYPE
```

Member of `PpMediaType` describing this shape.

The return value is unconditionally `PP_MEDIA_TYPE.MOVIE` in this case.

### `poster_frame`

```python
poster_frame
```

Return `Image` object containing poster frame for this movie.

Returns `None` if this movie has no poster frame (uncommon).

### `shape_type`

```python
shape_type: MSO_SHAPE_TYPE
```

Return member of `MsoShapeType` describing this shape.

The return value is unconditionally `MSO_SHAPE_TYPE.MEDIA` in this
case.

## `Picture`

Bases: `_BasePicture`

A picture shape, one that places an image on a slide.

Based on the `p:pic` element.

### `auto_shape_type`

```python
auto_shape_type: MSO_SHAPE | None
```

Member of MSO_SHAPE indicating masking shape.

A picture can be masked by any of the so-called "auto-shapes" available in PowerPoint,
such as an ellipse or triangle. When a picture is masked by a shape, the shape assumes the
same dimensions as the picture and the portion of the picture outside the shape boundaries
does not appear. Note the default value for a newly-inserted picture is
`MSO_AUTO_SHAPE_TYPE.RECTANGLE`, which performs no cropping because the extents of the
rectangle exactly correspond to the extents of the picture.

The available shapes correspond to the members of `MsoAutoShapeType`.

The return value can also be `None`, indicating the picture either has no geometry (not
expected) or has custom geometry, like a freeform shape. A picture with no geometry will
have no visible representation on the slide, although it can be selected. This is because
without geometry, there is no "inside-the-shape" for it to appear in.

### `image`

```python
image
```

The `Image` object for this picture.

Provides access to the properties and bytes of the image in this picture shape.

### `replace_image`

```python
replace_image(image_file: str | IO[bytes], *, allow_format_change: bool = False) -> None
```

Replace the image behind this picture, preserving its geometry exactly.

paper-pptx addition. Position, size, rotation, masking geometry, and crop
(`a:srcRect`) are not touched — only the `a:blip/@r:embed` target changes. By
default the new image's canonical format must match the existing image part's
extension (jpg == jpeg); a mismatch refuses with `UnsupportedStructureError`.
Passing `allow_format_change=True` permits a cross-format swap: the new
image gets its own correctly-typed part and `[Content_Types].xml` follows
automatically at save (it is regenerated from live parts).

A picture with no embedded image relationship (e.g. linked-only) refuses. The new
image part is deduplicated package-wide by content hash; the old image part simply
becomes unreferenced when this picture held its last reference (an unreachable part
is never serialized).

### `shape_type`

```python
shape_type: MSO_SHAPE_TYPE
```

Unconditionally `MSO_SHAPE_TYPE.PICTURE` in this case.
