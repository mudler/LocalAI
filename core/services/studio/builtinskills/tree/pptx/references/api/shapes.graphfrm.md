<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shapes.graphfrm`

Graphic Frame shape and related objects.

## `GraphicFrame`

```python
GraphicFrame(graphicFrame: CT_GraphicalObjectFrame, parent: ProvidesPart)
```

Bases: `BaseShape`

Container shape for table, chart, smart art, and media objects.

Corresponds to a `p:graphicFrame` element in the shape tree.

### `chart`

```python
chart: Chart
```

The `Chart` object containing the chart in this graphic frame.

Raises `ValueError` if this graphic frame does not contain a chart.

### `chart_part`

```python
chart_part: ChartPart
```

The `ChartPart` object containing the chart in this graphic frame.

### `has_chart`

```python
has_chart: bool
```

`True` if this graphic frame contains a chart object. `False` otherwise.

When `True`, the chart object can be accessed using the `.chart` property.

### `has_table`

```python
has_table: bool
```

`True` if this graphic frame contains a table object, `False` otherwise.

When `True`, the table object can be accessed using the `.table` property.

### `ole_format`

```python
ole_format: _OleFormat
```

_OleFormat object for this graphic-frame shape.

Raises `ValueError` on a GraphicFrame instance that does not contain an OLE object.

An shape that contains an OLE object will have `.shape_type` of either
`EMBEDDED_OLE_OBJECT` or `LINKED_OLE_OBJECT`.

### `shadow`

```python
shadow() -> ShadowFormat
```

Unconditionally raises `NotImplementedError`.

Access to the shadow effect for graphic-frame objects is content-specific (i.e. different
for charts, tables, etc.) and has not yet been implemented.

### `shape_type`

```python
shape_type: MSO_SHAPE_TYPE
```

Optional member of `MSO_SHAPE_TYPE` identifying the type of this shape.

Possible values are `MSO_SHAPE_TYPE.CHART`, `MSO_SHAPE_TYPE.TABLE`,
`MSO_SHAPE_TYPE.EMBEDDED_OLE_OBJECT`, `MSO_SHAPE_TYPE.LINKED_OLE_OBJECT`.

This value is `None` when none of these four types apply, for example when the shape
contains SmartArt.

### `table`

```python
table: Table
```

The `Table` object contained in this graphic frame.

Raises `ValueError` if this graphic frame does not contain a table.
