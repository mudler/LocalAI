<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.shape`

Custom element classes for shape-related elements like `<w:inline>`.

## `CT_Anchor`

Bases: `BaseOxmlElement`

`<wp:anchor>` element, container for a "floating" shape.

## `CT_Blip`

Bases: `BaseOxmlElement`

``<a:blip>`` element, specifies image source and adjustments such as alpha and
tint.

### `embed`

```python
embed: str | None = OptionalAttribute('r:embed', ST_RelationshipId)
```

### `link`

```python
link: str | None = OptionalAttribute('r:link', ST_RelationshipId)
```

## `CT_BlipFillProperties`

Bases: `BaseOxmlElement`

``<pic:blipFill>`` element, specifies picture properties.

### `blip`

```python
blip: CT_Blip = ZeroOrOne('a:blip', successors=('a:srcRect', 'a:tile', 'a:stretch'))
```

## `CT_GraphicalObject`

Bases: `BaseOxmlElement`

``<a:graphic>`` element, container for a DrawingML object.

### `graphicData`

```python
graphicData: CT_GraphicalObjectData = OneAndOnlyOne('a:graphicData')
```

## `CT_GraphicalObjectData`

Bases: `BaseOxmlElement`

``<a:graphicData>`` element, container for the XML of a DrawingML object.

### `pic`

```python
pic: CT_Picture = ZeroOrOne('pic:pic')
```

### `uri`

```python
uri: str = RequiredAttribute('uri', XsdToken)
```

## `CT_Inline`

Bases: `BaseOxmlElement`

`<wp:inline>` element, container for an inline shape.

### `docPr`

```python
docPr: CT_NonVisualDrawingProps = OneAndOnlyOne('wp:docPr')
```

### `extent`

```python
extent: CT_PositiveSize2D = OneAndOnlyOne('wp:extent')
```

### `graphic`

```python
graphic: CT_GraphicalObject = OneAndOnlyOne('a:graphic')
```

### `new`

```python
new(cx: Length, cy: Length, shape_id: int, pic: CT_Picture) -> CT_Inline
```

Return a new ``<wp:inline>`` element populated with the values passed as
parameters.

### `new_pic_inline`

```python
new_pic_inline(shape_id: int, rId: str, filename: str, cx: Length, cy: Length) -> CT_Inline
```

Create `wp:inline` element containing a `pic:pic` element.

The contents of the `pic:pic` element is taken from the argument values.

## `CT_NonVisualDrawingProps`

Bases: `BaseOxmlElement`

Used for ``<wp:docPr>`` element, and perhaps others.

Specifies the id and name of a DrawingML drawing.

### `id`

```python
id = RequiredAttribute('id', ST_DrawingElementId)
```

### `name`

```python
name = RequiredAttribute('name', XsdString)
```

## `CT_NonVisualPictureProperties`

Bases: `BaseOxmlElement`

``<pic:cNvPicPr>`` element, specifies picture locking and resize behaviors.

## `CT_Picture`

Bases: `BaseOxmlElement`

``<pic:pic>`` element, a DrawingML picture.

### `blipFill`

```python
blipFill: CT_BlipFillProperties = OneAndOnlyOne('pic:blipFill')
```

### `new`

```python
new(pic_id: int, filename: str, rId: str, cx: Length, cy: Length) -> CT_Picture
```

A new minimum viable `<pic:pic>` (picture) element.

### `nvPicPr`

```python
nvPicPr: CT_PictureNonVisual = OneAndOnlyOne('pic:nvPicPr')
```

### `spPr`

```python
spPr: CT_ShapeProperties = OneAndOnlyOne('pic:spPr')
```

## `CT_PictureNonVisual`

Bases: `BaseOxmlElement`

``<pic:nvPicPr>`` element, non-visual picture properties.

### `cNvPr`

```python
cNvPr = OneAndOnlyOne('pic:cNvPr')
```

## `CT_Point2D`

Bases: `BaseOxmlElement`

Used for ``<a:off>`` element, and perhaps others.

Specifies an x, y coordinate (point).

### `x`

```python
x = RequiredAttribute('x', ST_Coordinate)
```

### `y`

```python
y = RequiredAttribute('y', ST_Coordinate)
```

## `CT_PositiveSize2D`

Bases: `BaseOxmlElement`

Used for ``<wp:extent>`` element, and perhaps others later.

Specifies the size of a DrawingML drawing.

### `cx`

```python
cx: Length = RequiredAttribute('cx', ST_PositiveCoordinate)
```

### `cy`

```python
cy: Length = RequiredAttribute('cy', ST_PositiveCoordinate)
```

## `CT_PresetGeometry2D`

Bases: `BaseOxmlElement`

``<a:prstGeom>`` element, specifies an preset autoshape geometry, such as
``rect``.

## `CT_RelativeRect`

Bases: `BaseOxmlElement`

``<a:fillRect>`` element, specifying picture should fill containing rectangle
shape.

## `CT_ShapeProperties`

Bases: `BaseOxmlElement`

``<pic:spPr>`` element, specifies size and shape of picture container.

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

### `xfrm`

```python
xfrm = ZeroOrOne('a:xfrm', successors=('a:custGeom', 'a:prstGeom', 'a:ln', 'a:effectLst', 'a:effectDag', 'a:scene3d', 'a:sp3d', 'a:extLst'))
```

## `CT_StretchInfoProperties`

Bases: `BaseOxmlElement`

``<a:stretch>`` element, specifies how picture should fill its containing
shape.

## `CT_Transform2D`

Bases: `BaseOxmlElement`

``<a:xfrm>`` element, specifies size and shape of picture container.

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
ext = ZeroOrOne('a:ext', successors=())
```

### `off`

```python
off = ZeroOrOne('a:off', successors=('a:ext',))
```
