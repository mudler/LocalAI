<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.shapes.autoshape`

lxml custom element classes for shape-related XML elements.

## `CT_AdjPoint2D`

Bases: `BaseOxmlElement`

`a:pt` custom element class.

### `x`

```python
x: Length = RequiredAttribute('x', ST_Coordinate)
```

### `y`

```python
y: Length = RequiredAttribute('y', ST_Coordinate)
```

## `CT_CustomGeometry2D`

Bases: `BaseOxmlElement`

`a:custGeom` custom element class.

### `get_or_add_pathLst`

```python
get_or_add_pathLst: Callable[[], CT_Path2DList]
```

### `pathLst`

```python
pathLst: CT_Path2DList | None = ZeroOrOne('a:pathLst', successors=_tag_seq[6:])
```

## `CT_GeomGuide`

Bases: `BaseOxmlElement`

`a:gd` custom element class.

Defines a "guide", corresponding to a yellow diamond-shaped handle on an autoshape.

### `fmla`

```python
fmla: str = RequiredAttribute('fmla', XsdString)
```

### `name`

```python
name: str = RequiredAttribute('name', XsdString)
```

## `CT_GeomGuideList`

Bases: `BaseOxmlElement`

`a:avLst` custom element class.

### `gd`

```python
gd = ZeroOrMore('a:gd')
```

### `gd_lst`

```python
gd_lst: list[CT_GeomGuide]
```

## `CT_NonVisualDrawingShapeProps`

Bases: `BaseShapeElement`

`p:cNvSpPr` custom element class.

### `spLocks`

```python
spLocks = ZeroOrOne('a:spLocks')
```

### `txBox`

```python
txBox: bool | None = OptionalAttribute('txBox', XsdBoolean)
```

## `CT_Path2D`

Bases: `BaseOxmlElement`

`a:path` custom element class.

### `add_close`

```python
add_close() -> CT_Path2DClose
```

Return a newly created `a:close` element.

The new `a:close` element is appended to this `a:path` element.

### `add_lnTo`

```python
add_lnTo(x: Length, y: Length) -> CT_Path2DLineTo
```

Return a newly created `a:lnTo` subtree with end point *(x, y)*.

The new `a:lnTo` element is appended to this `a:path` element.

### `add_moveTo`

```python
add_moveTo(x: Length, y: Length)
```

Return a newly created `a:moveTo` subtree with point `(x, y)`.

The new `a:moveTo` element is appended to this `a:path` element.

### `close`

```python
close = ZeroOrMore('a:close', successors=())
```

### `h`

```python
h: Length | None = OptionalAttribute('h', ST_PositiveCoordinate)
```

### `lnTo`

```python
lnTo = ZeroOrMore('a:lnTo', successors=())
```

### `moveTo`

```python
moveTo = ZeroOrMore('a:moveTo', successors=())
```

### `w`

```python
w: Length | None = OptionalAttribute('w', ST_PositiveCoordinate)
```

## `CT_Path2DClose`

Bases: `BaseOxmlElement`

`a:close` custom element class.

## `CT_Path2DLineTo`

Bases: `BaseOxmlElement`

`a:lnTo` custom element class.

### `pt`

```python
pt = ZeroOrOne('a:pt', successors=())
```

## `CT_Path2DList`

Bases: `BaseOxmlElement`

`a:pathLst` custom element class.

### `add_path`

```python
add_path(w: Length, h: Length)
```

Return a newly created `a:path` child element.

### `path`

```python
path = ZeroOrMore('a:path', successors=())
```

## `CT_Path2DMoveTo`

Bases: `BaseOxmlElement`

`a:moveTo` custom element class.

### `pt`

```python
pt = ZeroOrOne('a:pt', successors=())
```

## `CT_PresetGeometry2D`

Bases: `BaseOxmlElement`

`a:prstGeom` custom element class.

### `avLst`

```python
avLst: CT_GeomGuideList | None = ZeroOrOne('a:avLst')
```

### `gd_lst`

```python
gd_lst: list[CT_GeomGuide]
```

Sequence of `a:gd` element children of `a:avLst`. Empty if none are present.

### `prst`

```python
prst: MSO_AUTO_SHAPE_TYPE = RequiredAttribute('prst', MSO_AUTO_SHAPE_TYPE)
```

### `rewrite_guides`

```python
rewrite_guides(guides: list[tuple[str, int]])
```

Replace any `a:gd` element children of `a:avLst` with ones forme from `guides`.

## `CT_Shape`

Bases: `BaseShapeElement`

`p:sp` custom element class.

### `add_path`

```python
add_path(w: Length, h: Length) -> CT_Path2D
```

### `get_or_add_ln`

```python
get_or_add_ln()
```

Return the `a:ln` grandchild element, newly added if not present.

### `get_or_add_txBody`

```python
get_or_add_txBody: Callable[[], CT_TextBody]
```

### `has_custom_geometry`

```python
has_custom_geometry
```

True if this shape has custom geometry, i.e. is a freeform shape.

A shape has custom geometry if it has a `p:spPr/a:custGeom`
descendant (instead of `p:spPr/a:prstGeom`).

### `is_autoshape`

```python
is_autoshape
```

True if this shape is an auto shape.

A shape is an auto shape if it has a `a:prstGeom` element and does not have a txBox="1"
attribute on cNvSpPr.

### `is_textbox`

```python
is_textbox
```

True if this shape is a text box.

A shape is a text box if it has a `txBox` attribute on cNvSpPr that resolves to `True`.
The default when the txBox attribute is missing is `False`.

### `ln`

```python
ln
```

`a:ln` grand-child element or `None` if not present.

### `new_autoshape_sp`

```python
new_autoshape_sp(id_: int, name: str, prst: str, left: int, top: int, width: int, height: int) -> CT_Shape
```

Return a new `p:sp` element tree configured as a base auto shape.

### `new_freeform_sp`

```python
new_freeform_sp(shape_id: int, name: str, x: int, y: int, cx: int, cy: int)
```

Return new `p:sp` element tree configured as freeform shape.

The returned shape has a `a:custGeom` subtree but no paths in its
path list.

### `new_placeholder_sp`

```python
new_placeholder_sp(id_: int, name: str, ph_type: PP_PLACEHOLDER, orient: str, sz: str, idx: str) -> CT_Shape
```

Return a new `p:sp` element tree configured as a placeholder shape.

### `new_textbox_sp`

```python
new_textbox_sp(id_, name, left, top, width, height)
```

Return a new `p:sp` element tree configured as a base textbox shape.

### `nvSpPr`

```python
nvSpPr: CT_ShapeNonVisual = OneAndOnlyOne('p:nvSpPr')
```

### `prst`

```python
prst
```

Value of `prst` attribute of `a:prstGeom` element or `None` if not present.

### `prstGeom`

```python
prstGeom: CT_PresetGeometry2D
```

Reference to `a:prstGeom` child element.

`None` if this shape doesn't have one, for example, if it's a placeholder shape.

### `spPr`

```python
spPr: CT_ShapeProperties = OneAndOnlyOne('p:spPr')
```

### `txBody`

```python
txBody: CT_TextBody | None = ZeroOrOne('p:txBody', successors=('p:extLst',))
```

## `CT_ShapeNonVisual`

Bases: `BaseShapeElement`

`p:nvSpPr` custom element class.

### `cNvPr`

```python
cNvPr: CT_NonVisualDrawingProps = OneAndOnlyOne('p:cNvPr')
```

### `cNvSpPr`

```python
cNvSpPr: CT_NonVisualDrawingShapeProps = OneAndOnlyOne('p:cNvSpPr')
```

### `nvPr`

```python
nvPr: CT_ApplicationNonVisualDrawingProps = OneAndOnlyOne('p:nvPr')
```
