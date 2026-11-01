<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.chart.chart`

Custom element classes for top-level chart-related XML elements.

## `CT_Chart`

Bases: `BaseOxmlElement`

`c:chart` custom element class.

### `autoTitleDeleted`

```python
autoTitleDeleted = ZeroOrOne('c:autoTitleDeleted', successors=_tag_seq[2:])
```

### `has_legend`

```python
has_legend
```

True if this chart has a legend defined, False otherwise.

### `legend`

```python
legend = ZeroOrOne('c:legend', successors=_tag_seq[9:])
```

### `new_chart`

```python
new_chart(rId: str) -> CT_Chart
```

Return a new `c:chart` element.

### `plotArea`

```python
plotArea = OneAndOnlyOne('c:plotArea')
```

### `rId`

```python
rId: str = RequiredAttribute('r:id', XsdString)
```

### `title`

```python
title = ZeroOrOne('c:title', successors=_tag_seq[1:])
```

## `CT_ChartSpace`

Bases: `BaseOxmlElement`

`c:chartSpace` root element of a chart part.

### `catAx_lst`

```python
catAx_lst
```

### `chart`

```python
chart = OneAndOnlyOne('c:chart')
```

### `date1904`

```python
date1904 = ZeroOrOne('c:date1904', successors=_tag_seq[1:])
```

### `dateAx_lst`

```python
dateAx_lst
```

### `date_1904`

```python
date_1904
```

Return `True` if the `c:date1904` child element resolves truthy,
`False` otherwise. This value indicates whether date number values
are based on the 1900 or 1904 epoch.

### `externalData`

```python
externalData = ZeroOrOne('c:externalData', successors=_tag_seq[11:])
```

### `get_or_add_title`

```python
get_or_add_title()
```

Return the `c:title` grandchild, newly created if not present.

### `plotArea`

```python
plotArea
```

Return the required `c:chartSpace/c:chart/c:plotArea` grandchild
element.

### `style`

```python
style = ZeroOrOne('c:style', successors=_tag_seq[4:])
```

### `txPr`

```python
txPr = ZeroOrOne('c:txPr', successors=_tag_seq[10:])
```

### `valAx_lst`

```python
valAx_lst
```

### `xlsx_part_rId`

```python
xlsx_part_rId
```

The string in the required ``r:id`` attribute of the
`<c:externalData>` child, or `None` if no externalData element is
present.

## `CT_ExternalData`

Bases: `BaseOxmlElement`

`<c:externalData>` element, defining link to embedded Excel package part
containing the chart data.

### `autoUpdate`

```python
autoUpdate = ZeroOrOne('c:autoUpdate')
```

### `rId`

```python
rId = RequiredAttribute('r:id', XsdString)
```

## `CT_PlotArea`

Bases: `BaseOxmlElement`

``<c:plotArea>`` element.

### `catAx`

```python
catAx = ZeroOrMore('c:catAx')
```

### `iter_sers`

```python
iter_sers()
```

Generate each of the `c:ser` elements in this chart, ordered first by
the document order of the containing xChart element, then by their
ordering within the xChart element (not necessarily document order).

### `iter_xCharts`

```python
iter_xCharts()
```

Generate each xChart child element in document.

### `last_ser`

```python
last_ser
```

Return the last `<c:ser>` element in the last xChart element, based
on series order (not necessarily the same element as document order).

### `next_idx`

```python
next_idx
```

Return the next available `c:ser/c:idx` value within the scope of
this chart, the maximum idx value found on existing series,
incremented by one.

### `next_order`

```python
next_order
```

Return the next available `c:ser/c:order` value within the scope of
this chart, the maximum order value found on existing series,
incremented by one.

### `sers`

```python
sers
```

Return a sequence containing all the `c:ser` elements in this chart,
ordered first by the document order of the containing xChart element,
then by their ordering within the xChart element (not necessarily
document order).

### `valAx`

```python
valAx = ZeroOrMore('c:valAx')
```

### `xCharts`

```python
xCharts
```

Return a sequence containing all the `c:{x}Chart` elements in this
chart, in document order.

## `CT_Style`

Bases: `BaseOxmlElement`

``<c:style>`` element; defines the chart style.

### `val`

```python
val = RequiredAttribute('val', ST_Style)
```
