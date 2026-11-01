<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.shapes.graphfrm`

lxml custom element class for CT_GraphicalObjectFrame XML element.

## `CT_GraphicalObject`

Bases: `BaseOxmlElement`

`a:graphic` element.

The container for the reference to or definition of the framed graphical object (table, chart,
etc.).

### `chart`

```python
chart: CT_Chart | None
```

The `c:chart` grandchild element, or `None` if not present.

### `graphicData`

```python
graphicData: CT_GraphicalObjectData = OneAndOnlyOne('a:graphicData')
```

## `CT_GraphicalObjectData`

Bases: `BaseShapeElement`

`p:graphicData` element.

The direct container for a table, a chart, or another graphical object.

### `blob_rId`

```python
blob_rId: str | None
```

Optional `r:id` attribute value of `p:oleObj` descendent element.

This value is `None` when this `p:graphicData` element does not enclose an OLE object.
This value could also be `None` if an enclosed OLE object does not specify this attribute
(it is specified optional in the schema) but so far, all OLE objects we've encountered
specify this value.

### `chart`

```python
chart: CT_Chart | None = ZeroOrOne('c:chart')
```

### `is_embedded_ole_obj`

```python
is_embedded_ole_obj: bool | None
```

Optional boolean indicating an embedded OLE object.

Returns `None` when this `p:graphicData` element does not enclose an OLE object. `True`
indicates an embedded OLE object and `False` indicates a linked OLE object.

### `progId`

```python
progId: str | None
```

Optional str value of "progId" attribute of `p:oleObj` descendent.

This value identifies the "type" of the embedded object in terms of the application used
to open it.

This value is `None` when this `p:graphicData` element does not enclose an OLE object.
This could also be `None` if an enclosed OLE object does not specify this attribute (it is
specified optional in the schema) but so far, all OLE objects we've encountered specify
this value.

### `showAsIcon`

```python
showAsIcon: bool | None
```

Optional value of "showAsIcon" attribute value of `p:oleObj` descendent.

This value is `None` when this `p:graphicData` element does not enclose an OLE object. It
is False when the `showAsIcon` attribute is omitted on the `p:oleObj` element.

### `tbl`

```python
tbl: CT_Table | None = ZeroOrOne('a:tbl')
```

### `uri`

```python
uri: str = RequiredAttribute('uri', XsdString)
```

## `CT_GraphicalObjectFrame`

Bases: `BaseShapeElement`

`p:graphicFrame` element.

A container for a table, a chart, or another graphical object.

### `chart`

```python
chart: CT_Chart | None
```

The `c:chart` great-grandchild element, or `None` if not present.

### `chart_rId`

```python
chart_rId: str | None
```

The `rId` attribute of the `c:chart` great-grandchild element.

`None` if not present.

### `get_or_add_xfrm`

```python
get_or_add_xfrm() -> CT_Transform2D
```

Return the required `p:xfrm` child element.

Overrides version on BaseShapeElement.

### `graphic`

```python
graphic: CT_GraphicalObject = OneAndOnlyOne('a:graphic')
```

### `graphicData`

```python
graphicData: CT_GraphicalObjectData
```

`a:graphicData` grandchild of this graphic-frame element.

### `graphicData_uri`

```python
graphicData_uri: str
```

str value of `uri` attribute of `a:graphicData` grandchild.

### `has_oleobj`

```python
has_oleobj: bool
```

`True` for graphicFrame containing an OLE object, `False` otherwise.

### `is_embedded_ole_obj`

```python
is_embedded_ole_obj: bool | None
```

Optional boolean indicating an embedded OLE object.

Returns `None` when this `p:graphicFrame` element does not enclose an OLE object. `True`
indicates an embedded OLE object and `False` indicates a linked OLE object.

### `new_chart_graphicFrame`

```python
new_chart_graphicFrame(id_: int, name: str, rId: str, x: int, y: int, cx: int, cy: int) -> CT_GraphicalObjectFrame
```

Return a `p:graphicFrame` element tree populated with a chart element.

### `new_graphicFrame`

```python
new_graphicFrame(id_: int, name: str, x: int, y: int, cx: int, cy: int) -> CT_GraphicalObjectFrame
```

Return a new `p:graphicFrame` element tree suitable for containing a table or chart.

Note that a graphicFrame element is not a valid shape until it contains a graphical object
such as a table.

### `new_ole_object_graphicFrame`

```python
new_ole_object_graphicFrame(id_: int, name: str, ole_object_rId: str, progId: str, icon_rId: str, x: int, y: int, cx: int, cy: int, imgW: int, imgH: int) -> CT_GraphicalObjectFrame
```

Return newly-created `p:graphicFrame` for embedded OLE-object.

`ole_object_rId` identifies the relationship to the OLE-object part.

`progId` is a str identifying the object-type in terms of the application (program) used
to open it. This becomes an attribute of the same name in the `p:oleObj` element.

`icon_rId` identifies the relationship to an image part used to display the OLE-object as
an icon (vs. a preview).

### `new_table_graphicFrame`

```python
new_table_graphicFrame(id_: int, name: str, rows: int, cols: int, x: int, y: int, cx: int, cy: int) -> CT_GraphicalObjectFrame
```

Return a `p:graphicFrame` element tree populated with a table element.

### `nvGraphicFramePr`

```python
nvGraphicFramePr: CT_GraphicalObjectFrameNonVisual = OneAndOnlyOne('p:nvGraphicFramePr')
```

### `xfrm`

```python
xfrm: CT_Transform2D = OneAndOnlyOne('p:xfrm')
```

## `CT_GraphicalObjectFrameNonVisual`

Bases: `BaseOxmlElement`

`p:nvGraphicFramePr` element.

This contains the non-visual properties of a graphic frame, such as name, id, etc.

### `cNvPr`

```python
cNvPr: CT_NonVisualDrawingProps = OneAndOnlyOne('p:cNvPr')
```

### `nvPr`

```python
nvPr: CT_ApplicationNonVisualDrawingProps = OneAndOnlyOne('p:nvPr')
```

## `CT_OleObject`

Bases: `BaseOxmlElement`

`p:oleObj` element, container for an OLE object (e.g. Excel file).

An OLE object can be either linked or embedded (hence the name).

### `is_embedded`

```python
is_embedded: bool
```

True when this OLE object is embedded, False when it is linked.

### `progId`

```python
progId: str | None = OptionalAttribute('progId', XsdString)
```

### `rId`

```python
rId: str | None = OptionalAttribute('r:id', XsdString)
```

### `showAsIcon`

```python
showAsIcon: bool = OptionalAttribute('showAsIcon', XsdBoolean, default=False)
```
