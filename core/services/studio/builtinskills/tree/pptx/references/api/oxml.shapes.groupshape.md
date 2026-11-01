<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.shapes.groupshape`

lxml custom element classes for shape-tree-related XML elements.

## `CT_GroupShape`

Bases: `BaseShapeElement`

Used for shape tree (`p:spTree`) as well as the group shape (`p:grpSp`) elements.

### `add_autoshape`

```python
add_autoshape(id_: int, name: str, prst: str, x: int, y: int, cx: int, cy: int) -> CT_Shape
```

Return new `p:sp` appended to the group/shapetree with specified attributes.

### `add_cxnSp`

```python
add_cxnSp(id_: int, name: str, type_member: MSO_CONNECTOR_TYPE, x: int, y: int, cx: int, cy: int, flipH: bool, flipV: bool) -> CT_Connector
```

Return new `p:cxnSp` appended to the group/shapetree with the specified attribues.

### `add_freeform_sp`

```python
add_freeform_sp(x: int, y: int, cx: int, cy: int) -> CT_Shape
```

Append a new freeform `p:sp` with specified position and size.

### `add_grpSp`

```python
add_grpSp() -> CT_GroupShape
```

Return `p:grpSp` element newly appended to this shape tree.

The element contains no sub-shapes, is positioned at (0, 0), and has
width and height of zero.

### `add_pic`

```python
add_pic(id_: int, name: str, desc: str, rId: str, x: int, y: int, cx: int, cy: int) -> CT_Picture
```

Append a `p:pic` shape to the group/shapetree having properties as specified in call.

### `add_placeholder`

```python
add_placeholder(id_: int, name: str, ph_type: PP_PLACEHOLDER, orient: str, sz: str, idx: int) -> CT_Shape
```

Append a newly-created placeholder `p:sp` shape having the specified properties.

### `add_table`

```python
add_table(id_: int, name: str, rows: int, cols: int, x: int, y: int, cx: int, cy: int) -> CT_GraphicalObjectFrame
```

Append a `p:graphicFrame` shape containing a table as specified in call.

### `add_textbox`

```python
add_textbox(id_: int, name: str, x: int, y: int, cx: int, cy: int) -> CT_Shape
```

Append a newly-created textbox `p:sp` shape having the specified position and size.

### `chExt`

```python
chExt
```

Descendent `p:grpSpPr/a:xfrm/a:chExt` element.

### `chOff`

```python
chOff
```

Descendent `p:grpSpPr/a:xfrm/a:chOff` element.

### `get_or_add_xfrm`

```python
get_or_add_xfrm() -> CT_Transform2D
```

Return the `a:xfrm` grandchild element, newly-added if not present.

### `grpSpPr`

```python
grpSpPr: CT_GroupShapeProperties = OneAndOnlyOne('p:grpSpPr')
```

### `iter_ph_elms`

```python
iter_ph_elms()
```

Generate each placeholder shape child element in document order.

### `iter_shape_elms`

```python
iter_shape_elms() -> Iterator[ShapeElement]
```

Generate each child of this `p:spTree` element that corresponds to a shape.

Items appear in XML document order.

### `max_shape_id`

```python
max_shape_id: int
```

Maximum int value assigned as @id in this slide.

This is generally a shape-id, but ids can be assigned to other
objects so we just check all @id values anywhere in the document
(XML id-values have document scope).

In practice, its minimum value is 1 because the spTree element itself
is always assigned id="1".

### `new_grpSp`

```python
new_grpSp(id_: int, name: str) -> CT_GroupShape
```

Return new "loose" `p:grpSp` element having `id_` and `name`.

### `nvGrpSpPr`

```python
nvGrpSpPr: CT_GroupShapeNonVisual = OneAndOnlyOne('p:nvGrpSpPr')
```

### `recalculate_extents`

```python
recalculate_extents() -> None
```

Adjust x, y, cx, and cy to incorporate all contained shapes.

This would typically be called when a contained shape is added,
removed, or its position or size updated.

This method is recursive "upwards" since a change in a group shape
can change the position and size of its containing group.

### `xfrm`

```python
xfrm: CT_Transform2D | None
```

The `a:xfrm` grandchild element or `None` if not found.

## `CT_GroupShapeNonVisual`

Bases: `BaseShapeElement`

`p:nvGrpSpPr` element.

### `cNvPr`

```python
cNvPr = OneAndOnlyOne('p:cNvPr')
```

## `CT_GroupShapeProperties`

Bases: `BaseOxmlElement`

p:grpSpPr element

### `effectLst`

```python
effectLst = ZeroOrOne('a:effectLst', successors=_tag_seq[8:])
```

### `get_or_add_xfrm`

```python
get_or_add_xfrm: Callable[[], CT_Transform2D]
```

### `xfrm`

```python
xfrm: CT_Transform2D | None = ZeroOrOne('a:xfrm', successors=_tag_seq[1:])
```
