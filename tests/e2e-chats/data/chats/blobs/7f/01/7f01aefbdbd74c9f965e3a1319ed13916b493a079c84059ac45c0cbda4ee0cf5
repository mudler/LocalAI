<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.shapes.picture`

lxml custom element classes for picture-related XML elements.

## `CT_Picture`

Bases: `BaseShapeElement`

`p:pic` element.

Represents a picture shape (an image placement on a slide).

### `blipFill`

```python
blipFill = OneAndOnlyOne('p:blipFill')
```

### `blip_rId`

```python
blip_rId: str | None
```

Value of `p:blipFill/a:blip/@r:embed`.

Returns `None` if not present.

### `crop_to_fit`

```python
crop_to_fit(image_size, view_size)
```

Set cropping values in `p:blipFill/a:srcRect` such that an image of
*image_size* will stretch to exactly fit *view_size* when its aspect
ratio is preserved.

### `get_or_add_ln`

```python
get_or_add_ln()
```

Return the <a:ln> grandchild element, newly added if not present.

### `ln`

```python
ln
```

``<a:ln>`` grand-child element or `None` if not present

### `new_ph_pic`

```python
new_ph_pic(id_, name, desc, rId)
```

Return a new `p:pic` placeholder element populated with the supplied
parameters.

### `new_pic`

```python
new_pic(shape_id, name, desc, rId, x, y, cx, cy)
```

Return new `<p:pic>` element tree configured with supplied parameters.

### `new_video_pic`

```python
new_video_pic(shape_id: int, shape_name: str, video_rId: str, media_rId: str, poster_frame_rId: str, x: Length, y: Length, cx: Length, cy: Length) -> CT_Picture
```

Return a new `p:pic` populated with the specified video.

### `nvPicPr`

```python
nvPicPr = OneAndOnlyOne('p:nvPicPr')
```

### `spPr`

```python
spPr: CT_ShapeProperties = OneAndOnlyOne('p:spPr')
```

### `srcRect_b`

```python
srcRect_b
```

Value of `p:blipFill/a:srcRect/@b` or 0.0 if not present.

### `srcRect_l`

```python
srcRect_l
```

Value of `p:blipFill/a:srcRect/@l` or 0.0 if not present.

### `srcRect_r`

```python
srcRect_r
```

Value of `p:blipFill/a:srcRect/@r` or 0.0 if not present.

### `srcRect_t`

```python
srcRect_t
```

Value of `p:blipFill/a:srcRect/@t` or 0.0 if not present.

## `CT_PictureNonVisual`

Bases: `BaseOxmlElement`

``<p:nvPicPr>`` element, containing non-visual properties for a picture
shape.

### `cNvPr`

```python
cNvPr = OneAndOnlyOne('p:cNvPr')
```

### `nvPr`

```python
nvPr = OneAndOnlyOne('p:nvPr')
```
