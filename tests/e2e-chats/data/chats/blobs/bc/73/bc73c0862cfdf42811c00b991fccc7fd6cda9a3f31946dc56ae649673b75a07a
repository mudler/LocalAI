<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.shapes.shared`

Common shape-related oxml objects.

## `BaseShapeElement`

Bases: `BaseOxmlElement`

Provides common behavior for shape element classes like CT_Shape, CT_Picture, etc.

### `cx`

```python
cx: Length
```

### `cy`

```python
cy: Length
```

### `flipH`

```python
flipH
```

### `flipV`

```python
flipV
```

### `get_or_add_xfrm`

```python
get_or_add_xfrm()
```

Return the `a:xfrm` grandchild element, newly-added if not present.

This version works for `p:sp`, `p:cxnSp`, and `p:pic` elements, others will need to
override.

### `has_ph_elm`

```python
has_ph_elm
```

True if this shape element has a `p:ph` descendant, indicating it
is a placeholder shape. False otherwise.

### `ph`

```python
ph: CT_Placeholder | None
```

The `p:ph` descendant element if there is one, None otherwise.

### `ph_idx`

```python
ph_idx: int
```

Integer value of placeholder idx attribute.

Raises `ValueError` if shape is not a placeholder.

### `ph_orient`

```python
ph_orient: str
```

Placeholder orientation, e.g. 'vert'.

Raises `ValueError` if shape is not a placeholder.

### `ph_sz`

```python
ph_sz: str
```

Placeholder size, e.g. ST_PlaceholderSize.HALF.

Raises `ValueError` if shape is not a placeholder.

### `ph_type`

```python
ph_type
```

Placeholder type, e.g. ST_PlaceholderType.TITLE ('title').

Raises `ValueError` if shape is not a placeholder.

### `rot`

```python
rot: float
```

Float representing degrees this shape is rotated clockwise.

### `shape_id`

```python
shape_id
```

Integer id of this shape

### `shape_name`

```python
shape_name
```

Name of this shape

### `spPr`

```python
spPr: CT_ShapeProperties
```

### `txBody`

```python
txBody
```

Child `p:txBody` element, None if not present.

### `x`

```python
x: Length
```

### `xfrm`

```python
xfrm
```

The `a:xfrm` grandchild element or `None` if not found.

This version works for `p:sp`, `p:cxnSp`, and `p:pic` elements, others will need to
override.

### `y`

```python
y: Length
```

## `CT_ApplicationNonVisualDrawingProps`

Bases: `BaseOxmlElement`

`p:nvPr` element.

### `get_or_add_ph`

```python
get_or_add_ph: Callable[[], CT_Placeholder]
```

### `ph`

```python
ph = ZeroOrOne('p:ph', successors=('a:audioCd', 'a:wavAudioFile', 'a:audioFile', 'a:videoFile', 'a:quickTimeFile', 'p:custDataLst', 'p:extLst'))
```

## `CT_LineProperties`

Bases: `BaseOxmlElement`

Custom element class for <a:ln> element

### `custDash`

```python
custDash = ZeroOrOne('a:custDash', successors=_tag_seq[6:])
```

### `eg_fillProperties`

```python
eg_fillProperties
```

Required to fulfill the interface used by dml.fill.

### `eg_lineFillProperties`

```python
eg_lineFillProperties = ZeroOrOneChoice((Choice('a:noFill'), Choice('a:solidFill'), Choice('a:gradFill'), Choice('a:pattFill')), successors=_tag_seq[4:])
```

### `prstDash`

```python
prstDash = ZeroOrOne('a:prstDash', successors=_tag_seq[5:])
```

### `prstDash_val`

```python
prstDash_val
```

Return value of `val` attribute of `a:prstDash` child.

Return `None` if not present.

### `w`

```python
w = OptionalAttribute('w', ST_LineWidth, default=Emu(0))
```

## `CT_NonVisualDrawingProps`

Bases: `BaseOxmlElement`

`p:cNvPr` custom element class.

### `get_or_add_hlinkClick`

```python
get_or_add_hlinkClick: Callable[[], CT_Hyperlink]
```

### `get_or_add_hlinkHover`

```python
get_or_add_hlinkHover: Callable[[], CT_Hyperlink]
```

### `hlinkClick`

```python
hlinkClick: CT_Hyperlink | None = ZeroOrOne('a:hlinkClick', successors=_tag_seq[1:])
```

### `hlinkHover`

```python
hlinkHover: CT_Hyperlink | None = ZeroOrOne('a:hlinkHover', successors=_tag_seq[2:])
```

### `id`

```python
id = RequiredAttribute('id', ST_DrawingElementId)
```

### `name`

```python
name = RequiredAttribute('name', XsdString)
```

## `CT_Placeholder`

Bases: `BaseOxmlElement`

`p:ph` custom element class.

### `idx`

```python
idx: int = OptionalAttribute('idx', XsdUnsignedInt, default=0)
```

### `orient`

```python
orient: str = OptionalAttribute('orient', ST_Direction, default=ST_Direction.HORZ)
```

### `sz`

```python
sz: str = OptionalAttribute('sz', ST_PlaceholderSize, default=ST_PlaceholderSize.FULL)
```

### `type`

```python
type: PP_PLACEHOLDER = OptionalAttribute('type', PP_PLACEHOLDER, default=PP_PLACEHOLDER.OBJECT)
```

## `CT_Point2D`

Bases: `BaseOxmlElement`

Custom element class for <a:off> element.

### `x`

```python
x: Length = RequiredAttribute('x', ST_Coordinate)
```

### `y`

```python
y: Length = RequiredAttribute('y', ST_Coordinate)
```

## `CT_PositiveSize2D`

Bases: `BaseOxmlElement`

Custom element class for <a:ext> element.

### `cx`

```python
cx = RequiredAttribute('cx', ST_PositiveCoordinate)
```

### `cy`

```python
cy = RequiredAttribute('cy', ST_PositiveCoordinate)
```

## `CT_ShapeProperties`

Bases: `BaseOxmlElement`

Custom element class for `p:spPr` element.

Shared by `p:sp`, `p:cxnSp`,  and `p:pic` elements as well as a few more obscure ones.

### `custGeom`

```python
custGeom: CT_CustomGeometry2D | None = ZeroOrOne('a:custGeom', successors=_tag_seq[2:])
```

### `cx`

```python
cx
```

Shape width as an instance of Emu, or None if not present.

### `cy`

```python
cy
```

Shape height as an instance of Emu, or None if not present.

### `effectLst`

```python
effectLst = ZeroOrOne('a:effectLst', successors=_tag_seq[11:])
```

### `eg_fillProperties`

```python
eg_fillProperties = ZeroOrOneChoice((Choice('a:noFill'), Choice('a:solidFill'), Choice('a:gradFill'), Choice('a:blipFill'), Choice('a:pattFill'), Choice('a:grpFill')), successors=_tag_seq[9:])
```

### `get_or_add_ln`

```python
get_or_add_ln: Callable[[], CT_LineProperties]
```

### `get_or_add_xfrm`

```python
get_or_add_xfrm: Callable[[], CT_Transform2D]
```

### `ln`

```python
ln: CT_LineProperties | None = ZeroOrOne('a:ln', successors=_tag_seq[10:])
```

### `prstGeom`

```python
prstGeom: CT_PresetGeometry2D | None = ZeroOrOne('a:prstGeom', successors=_tag_seq[3:])
```

### `x`

```python
x: Length | None
```

Distance between the left edge of the slide and left edge of the shape.

0 if not present.

### `xfrm`

```python
xfrm: CT_Transform2D | None = ZeroOrOne('a:xfrm', successors=_tag_seq[1:])
```

### `y`

```python
y
```

The offset of the top of the shape from the top of the slide, as an
instance of Emu. None if not present.

## `CT_Transform2D`

Bases: `BaseOxmlElement`

`a:xfrm` custom element class.

NOTE: this is a composite including CT_GroupTransform2D, which appears
with the `a:xfrm` tag in a group shape (including a slide `p:spTree`).

### `chExt`

```python
chExt = ZeroOrOne('a:chExt', successors=_tag_seq[4:])
```

### `chOff`

```python
chOff = ZeroOrOne('a:chOff', successors=_tag_seq[3:])
```

### `cx`

```python
cx
```

### `cy`

```python
cy
```

### `ext`

```python
ext = ZeroOrOne('a:ext', successors=_tag_seq[2:])
```

### `flipH`

```python
flipH = OptionalAttribute('flipH', XsdBoolean, default=False)
```

### `flipV`

```python
flipV = OptionalAttribute('flipV', XsdBoolean, default=False)
```

### `off`

```python
off: CT_Point2D | None = ZeroOrOne('a:off', successors=_tag_seq[1:])
```

### `rot`

```python
rot: float | None = OptionalAttribute('rot', ST_Angle, default=0.0)
```

### `x`

```python
x
```

### `y`

```python
y
```
